// Device services: a ticking clock, the screen wake lock, haptics, theme.

class Clock {
  now = $state(Date.now());
  #users = 0;
  #id: ReturnType<typeof setInterval> | null = null;

  /** Tick while at least one caller needs it. Returns a release function. */
  use(): () => void {
    this.#users++;
    if (!this.#id) {
      this.now = Date.now();
      this.#id = setInterval(() => (this.now = Date.now()), 250);
    }
    return () => {
      this.#users--;
      if (this.#users === 0 && this.#id) {
        clearInterval(this.#id);
        this.#id = null;
      }
    };
  }
}

export const clock = new Clock();

/** Keep the screen on during a session (iOS 18.4+ in home-screen apps). */
export function keepAwake(): () => void {
  let lock: WakeLockSentinel | null = null;
  let released = false;
  const acquire = async () => {
    if (released || document.visibilityState !== 'visible' || !('wakeLock' in navigator)) return;
    try {
      lock = await navigator.wakeLock.request('screen');
    } catch {
      /* low battery mode or unsupported: the session still works */
    }
  };
  const onVis = () => void acquire();
  document.addEventListener('visibilitychange', onVis);
  void acquire();
  return () => {
    released = true;
    document.removeEventListener('visibilitychange', onVis);
    void lock?.release().catch(() => {});
  };
}

/** A short buzz where supported (Android; iOS Safari has no Vibration API). */
export function buzz(pattern: number | number[] = [180, 90, 180]): void {
  try {
    navigator.vibrate?.(pattern);
  } catch {
    /* unsupported */
  }
}

export type Theme = 'dark' | 'light';

class ThemeState {
  value = $state<Theme>('dark');

  constructor() {
    try {
      if (localStorage.getItem('domestique:theme') === 'light') this.value = 'light';
    } catch {
      /* storage blocked: stay dark */
    }
    this.#apply();
  }

  toggle(): void {
    this.value = this.value === 'dark' ? 'light' : 'dark';
    try {
      localStorage.setItem('domestique:theme', this.value);
    } catch {
      /* ignore */
    }
    this.#apply();
  }

  #apply(): void {
    document.documentElement.dataset.theme = this.value;
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', this.value === 'dark' ? '#16171a' : '#f4f1ea');
  }
}

export const theme = new ThemeState();
