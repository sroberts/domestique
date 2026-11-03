<script lang="ts">
  import CalendarDays from '@lucide/svelte/icons/calendar-days';
  import List from '@lucide/svelte/icons/list';
  import TrendingUp from '@lucide/svelte/icons/trending-up';
  import Settings from '@lucide/svelte/icons/settings-2';
  import { link, router } from '../lib/router.svelte';

  const tabs = [
    { href: '/', label: 'Today', icon: CalendarDays, match: ['today'] },
    { href: '/activities', label: 'Activities', icon: List, match: ['activities', 'activity', 'summary'] },
    { href: '/trends', label: 'Trends', icon: TrendingUp, match: ['trends'] },
    { href: '/settings', label: 'Settings', icon: Settings, match: ['settings'] },
  ];
</script>

<nav class="tabbar" aria-label="Sections">
  <ul>
    {#each tabs as t (t.href)}
      {@const current = t.match.includes(router.route.name)}
      <li>
        <a href={t.href} use:link aria-current={current ? 'page' : undefined} class:current>
          <t.icon size={22} strokeWidth={current ? 2.25 : 1.75} aria-hidden="true" />
          <span>{t.label}</span>
        </a>
      </li>
    {/each}
  </ul>
</nav>

<style>
  .tabbar {
    position: fixed;
    inset: auto 0 0 0;
    z-index: var(--z-sticky);
    background: var(--bg);
    border-top: 1px solid var(--line);
    padding-bottom: env(safe-area-inset-bottom);
  }
  ul {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    max-width: 35rem;
    margin: 0 auto;
    padding: 0;
    list-style: none;
  }
  a {
    display: grid;
    justify-items: center;
    align-content: center;
    gap: 0.125rem;
    height: var(--tabbar-h);
    color: var(--text-muted);
    text-decoration: none;
    font-size: 0.6875rem;
    font-weight: 600;
    letter-spacing: 0.02em;
  }
  a.current {
    color: var(--text);
  }
  a:active {
    background: var(--band);
  }
</style>
