<script lang="ts">
  // The route on a real map. MapLibre is loaded only when a ride with GPS is
  // opened, and tiles come from OpenFreeMap (no key, no tracking). Without a
  // connection the route still draws, on a blank canvas.
  import { onDestroy } from 'svelte';
  import { theme } from '../lib/device.svelte';
  import type { Map as MLMap, GeoJSONSource } from 'maplibre-gl';
  import type { ZwiftWorld } from '../lib/types';

  interface Props {
    route: [number, number][];
    cursor: [number, number] | null;
    /** The Zwift world the route is in, if any. */
    world?: ZwiftWorld;
    /** CSS colour token for the route line, e.g. the sport's colour. */
    tone?: string;
  }
  let { route, cursor, world, tone = '--text' }: Props = $props();

  // A made-up Zwift world sits over open ocean or unrelated land, so a real
  // map under it would mislead: draw Zwift's own map of the world instead,
  // which the server fetches from Zwift and keeps. If that fails, the route
  // goes on a plain background. Worlds that sit on the place they copy
  // (London, New York, ...) keep the real map.
  const madeUp = $derived(!!world && !world.real);

  const blank = () => ({ version: 8 as const, sources: {}, layers: [{ id: 'bg', type: 'background' as const, paint: { 'background-color': css('--surface') } }] });

  function zwiftStyle(w: ZwiftWorld) {
    if (!w.map_corners) return blank();
    // MapLibre takes [lng, lat]; the corners may be turned, as some of Zwift's images are.
    const [tl, tr, br, bl] = w.map_corners.map(([lat, lng]) => [lng, lat] as [number, number]);
    return {
      version: 8 as const,
      sources: {
        world: {
          type: 'image' as const,
          url: `/v1/worlds/${encodeURIComponent(w.slug)}/map`,
          coordinates: [tl!, tr!, br!, bl!] as [[number, number], [number, number], [number, number], [number, number]],
        },
      },
      layers: [
        { id: 'bg', type: 'background' as const, paint: { 'background-color': w.background || css('--surface') } },
        { id: 'world', type: 'raster' as const, source: 'world', paint: { 'raster-fade-duration': 0 } },
      ],
    };
  }

  let el = $state<HTMLDivElement | null>(null);
  let map: MLMap | null = null;
  let failed = $state(false);
  let ready = $state(false);

  const styleUrl = (t: string) => `https://tiles.openfreemap.org/styles/${t === 'dark' ? 'dark' : 'positron'}`;

  function css(name: string): string {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function addLayers(m: MLMap) {
    const coords = route.map(([lat, lng]) => [lng, lat]);
    if (!m.getSource('route')) {
      m.addSource('route', { type: 'geojson', data: { type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: coords } } });
      m.addSource('ends', {
        type: 'geojson',
        data: {
          type: 'FeatureCollection',
          features: [
            { type: 'Feature', properties: { k: 'start' }, geometry: { type: 'Point', coordinates: coords[0]! } },
            { type: 'Feature', properties: { k: 'end' }, geometry: { type: 'Point', coordinates: coords.at(-1)! } },
          ],
        },
      });
      m.addSource('cursor', { type: 'geojson', data: { type: 'FeatureCollection', features: [] } });
    }
    const ink = css(tone);
    const bg = css('--bg');
    m.addLayer({ id: 'route-casing', type: 'line', source: 'route', paint: { 'line-color': bg, 'line-width': 6, 'line-opacity': 0.8 }, layout: { 'line-join': 'round', 'line-cap': 'round' } });
    m.addLayer({ id: 'route', type: 'line', source: 'route', paint: { 'line-color': ink, 'line-width': 3 }, layout: { 'line-join': 'round', 'line-cap': 'round' } });
    m.addLayer({
      id: 'ends', type: 'circle', source: 'ends',
      paint: { 'circle-radius': 5, 'circle-color': ['match', ['get', 'k'], 'start', ink, bg], 'circle-stroke-color': ink, 'circle-stroke-width': 2 },
    });
    m.addLayer({ id: 'cursor', type: 'circle', source: 'cursor', paint: { 'circle-radius': 6, 'circle-color': css('--signal'), 'circle-stroke-color': bg, 'circle-stroke-width': 2 } });
  }

  function fit(m: MLMap) {
    let [minLat, minLng, maxLat, maxLng] = [90, 180, -90, -180];
    for (const [lat, lng] of route) {
      minLat = Math.min(minLat, lat);
      maxLat = Math.max(maxLat, lat);
      minLng = Math.min(minLng, lng);
      maxLng = Math.max(maxLng, lng);
    }
    m.fitBounds([[minLng, minLat], [maxLng, maxLat]], { padding: 28, duration: 0 });
  }

  $effect(() => {
    if (!el || map || route.length < 2) return;
    let cancelled = false;
    import('maplibre-gl')
      .then(async (ml) => {
        await import('maplibre-gl/dist/maplibre-gl.css');
        if (cancelled || !el) return;
        const m = new ml.Map({
          container: el,
          style: !navigator.onLine ? blank() : madeUp && world ? zwiftStyle(world) : styleUrl(theme.value),
          attributionControl: madeUp && world?.map_corners ? { compact: true, customAttribution: '© Zwift' } : { compact: true },
          dragRotate: false,
          pitchWithRotate: false,
          cooperativeGestures: true,
        });
        m.touchZoomRotate.disableRotation();
        m.on('load', () => {
          // Start the attribution folded to its "i" button; it opens on tap.
          el?.querySelector('.maplibregl-ctrl-attrib')?.classList.remove('maplibregl-compact-show');
          addLayers(m);
          fit(m);
          ready = true;
        });
        m.on('error', (e) => {
          // Zwift's map didn't come: fall back to the plain background.
          if ((e as { sourceId?: string }).sourceId === 'world' && m.getLayer('world')) {
            m.removeLayer('world');
            m.setPaintProperty('bg', 'background-color', css('--surface'));
            return;
          }
          // Tile failures are fine; a style failure means no basemap.
          if (!m.isStyleLoaded() && !ready) console.warn('map', e.error?.message);
        });
        map = m;
        // Rotation, split view and window resizes change the container under the map.
        const ro = new ResizeObserver(() => m.resize());
        ro.observe(el);
        m.once('remove', () => ro.disconnect());
      })
      .catch(() => (failed = true));
    return () => {
      cancelled = true;
    };
  });

  // Follow the theme.
  let lastTheme = theme.value;
  $effect(() => {
    const t = theme.value;
    if (!map || t === lastTheme || madeUp) return;
    lastTheme = t;
    map.setStyle(styleUrl(t));
    map.once('styledata', () => map && addLayers(map));
  });

  $effect(() => {
    if (!map || !ready) return;
    const src = map.getSource('cursor') as GeoJSONSource | undefined;
    src?.setData(
      cursor
        ? { type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: [cursor[1], cursor[0]] } }
        : { type: 'FeatureCollection', features: [] },
    );
  });

  onDestroy(() => map?.remove());
</script>

<div class="map" bind:this={el} role="img" aria-label={world ? `Map of the route in Zwift's ${world.name}` : 'Map of the route'}>
  {#if world}
    <p class="world"><span class="label">Zwift</span> {world.name}</p>
  {/if}
  {#if failed}
    <p class="muted fallback">The map couldn't load. The route is still in the charts below.</p>
  {/if}
</div>

<style>
  .map {
    position: relative;
    height: 16rem;
    margin: 0 calc(var(--gutter) * -1);
    background: var(--surface);
    border-top: 1px solid var(--line);
    border-bottom: 1px solid var(--line);
  }
  @media (min-width: 640px) {
    .map {
      height: 22rem;
      margin: 0;
      border: 1px solid var(--line);
      border-radius: var(--radius-m);
      overflow: hidden;
    }
  }
  .fallback {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
    padding: var(--space-4);
    text-align: center;
    font-size: var(--size-small);
  }
  .world {
    position: absolute;
    top: var(--space-2);
    left: var(--space-2);
    z-index: 1;
    margin: 0;
    padding: var(--space-1) var(--space-2);
    background: var(--surface-raised);
    border: 1px solid var(--line);
    border-radius: var(--radius-s);
    font-size: var(--size-small);
    color: var(--text);
    pointer-events: none;
  }
  .world .label {
    margin-right: var(--space-1);
  }
  .map :global(.maplibregl-ctrl-attrib) {
    font-size: 10px;
  }
</style>
