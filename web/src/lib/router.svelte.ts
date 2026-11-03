// A tiny history router: three routes don't need a library.

export type Route =
  | { name: 'today' }
  | { name: 'activities' }
  | { name: 'activity'; id: string }
  | { name: 'trends' }
  | { name: 'settings' }
  | { name: 'gear' }
  | { name: 'session'; id: string }
  | { name: 'summary'; id: string }
  | { name: 'notfound' };

function parse(path: string): Route {
  if (path === '/' || path === '') return { name: 'today' };
  if (path === '/activities') return { name: 'activities' };
  if (path === '/trends') return { name: 'trends' };
  if (path === '/settings') return { name: 'settings' };
  if (path === '/gear') return { name: 'gear' };
  const a = /^\/activity\/([A-Za-z0-9_-]+)\/?$/.exec(path);
  if (a?.[1]) return { name: 'activity', id: a[1] };
  let m = /^\/session\/([A-Za-z0-9_-]+)\/?$/.exec(path);
  if (m?.[1]) return { name: 'session', id: m[1] };
  m = /^\/session\/([A-Za-z0-9_-]+)\/summary\/?$/.exec(path);
  if (m?.[1]) return { name: 'summary', id: m[1] };
  return { name: 'notfound' };
}

class Router {
  route = $state<Route>(parse(location.pathname));
  /** The in-app path before the current one, for context-aware Back links. */
  prev = $state<string | null>(null);
  /** Live-session screens hide the tab bar: one action per state. */
  get chrome(): boolean {
    return this.route.name !== 'session';
  }

  constructor() {
    window.addEventListener('popstate', () => {
      this.route = parse(location.pathname);
    });
  }

  go(path: string, opts: { replace?: boolean } = {}): void {
    if (!opts.replace) this.prev = location.pathname;
    if (opts.replace) history.replaceState(null, '', path);
    else history.pushState(null, '', path);
    this.route = parse(path.split(/[?#]/)[0] ?? '/');
    const hash = path.split('#')[1];
    window.scrollTo(0, 0);
    if (hash) scrollToWhenReady(hash);
  }
}

/** The target screen may still be loading; look for the anchor for a second. */
function scrollToWhenReady(id: string, tries = 60): void {
  const el = document.getElementById(id);
  if (el) el.scrollIntoView({ block: 'start' });
  else if (tries > 0) requestAnimationFrame(() => scrollToWhenReady(id, tries - 1));
}

export const router = new Router();

/** Where a Back link should go: the previous list screen, else a default. */
export function backTarget(fallback: { href: string; label: string }): { href: string; label: string } {
  const p = router.prev;
  if (p === '/activities') return { href: p, label: 'Activities' };
  if (p === '/trends') return { href: p, label: 'Trends' };
  if (p === '/') return { href: p, label: 'Today' };
  return fallback;
}

/** Svelte action: make an <a href> navigate in-app. */
export function link(node: HTMLAnchorElement) {
  const onClick = (e: MouseEvent) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    router.go(node.getAttribute('href') ?? '/');
  };
  node.addEventListener('click', onClick);
  return { destroy: () => node.removeEventListener('click', onClick) };
}
