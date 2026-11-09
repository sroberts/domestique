// While Outpost's models write something in the background, a screen asks
// again a few times; what it has stands meanwhile, and for good if the model
// never answers.
export class Recheck {
  private n = 0;
  private t: ReturnType<typeof setTimeout> | undefined;

  constructor(
    private readonly again: () => void,
    private readonly every = 20_000,
    private readonly times = 9,
  ) {}

  /** Call after each load with whether anything is still being written. */
  after(pending: boolean): void {
    clearTimeout(this.t);
    if (pending && this.n < this.times) {
      this.n++;
      this.t = setTimeout(() => {
        if (document.visibilityState === 'visible') this.again();
        else this.after(true);
      }, this.every);
    }
  }

  stop(): void {
    clearTimeout(this.t);
  }
}
