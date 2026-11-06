<script lang="ts">
  import { router } from './lib/router.svelte';
  import Today from './screens/Today.svelte';
  import SessionScreen from './screens/Session.svelte';
  import SummaryScreen from './screens/Summary.svelte';
  import Activities from './screens/Activities.svelte';
  import ActivityScreen from './screens/Activity.svelte';
  import TrendsScreen from './screens/Trends.svelte';
  import SettingsScreen from './screens/Settings.svelte';
  import GearScreen from './screens/Gear.svelte';
  import TabBar from './components/TabBar.svelte';
  import Workouts from './screens/Workouts.svelte';
  import WorkoutEdit from './screens/WorkoutEdit.svelte';
</script>

{#if router.route.name === 'today'}
  <Today />
{:else if router.route.name === 'session'}
  {#key router.route.id}
    <SessionScreen id={router.route.id} />
  {/key}
{:else if router.route.name === 'activities'}
  <Activities />
{:else if router.route.name === 'activity'}
  {#key router.route.id}
    <ActivityScreen id={router.route.id} />
  {/key}
{:else if router.route.name === 'trends'}
  <TrendsScreen />
{:else if router.route.name === 'settings'}
  <SettingsScreen />
{:else if router.route.name === 'gear'}
  <GearScreen />
{:else if router.route.name === 'summary'}
  {#key router.route.id}
    <SummaryScreen id={router.route.id} />
  {/key}
{:else if router.route.name === 'workouts'}
  <Workouts />
{:else if router.route.name === 'workout'}
  {#key router.route.id}
    <WorkoutEdit id={router.route.id} />
  {/key}
{:else}
  <main class="notfound">
    <h1>Nothing on this road.</h1>
    <p class="muted">That page doesn't exist.</p>
    <a class="btn" href="/">Back to today</a>
  </main>
{/if}

{#if router.chrome}
  <div class="tab-space" aria-hidden="true"></div>
  <TabBar />
{/if}

<style>
  .tab-space {
    height: calc(var(--tabbar-h) + env(safe-area-inset-bottom) + var(--space-5));
  }
  .notfound {
    display: grid;
    gap: var(--space-4);
    justify-items: start;
    padding: calc(env(safe-area-inset-top) + var(--space-7)) var(--gutter);
    max-width: 35rem;
    margin: 0 auto;
  }
</style>
