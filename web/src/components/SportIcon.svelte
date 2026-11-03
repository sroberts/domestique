<script lang="ts">
  import Bike from '@lucide/svelte/icons/bike';
  import Monitor from '@lucide/svelte/icons/monitor';
  import Dumbbell from '@lucide/svelte/icons/dumbbell';
  import Footprints from '@lucide/svelte/icons/footprints';
  import Mountain from '@lucide/svelte/icons/mountain';
  import Waves from '@lucide/svelte/icons/waves';
  import Activity from '@lucide/svelte/icons/activity';
  import { SPORT_LABEL } from '../lib/format';

  let { sport, size = 20 }: { sport: string; size?: number } = $props();
  const icons: Record<string, typeof Bike> = {
    ride: Bike, virtual_ride: Monitor, strength: Dumbbell, run: Footprints, walk: Footprints, hike: Mountain, swim: Waves,
  };
  let Icon = $derived(icons[sport] ?? Activity);
</script>

<Icon {size} strokeWidth={1.75} aria-label={SPORT_LABEL[sport] ?? 'Workout'} role="img" />
