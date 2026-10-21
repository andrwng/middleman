// Sorted set of file indices, for DiffView's active-file tracking: the
// indices of files whose top has crossed the active line. The active file
// is the last of them in list order, so last() is O(1); add and delete
// run only when a file crosses the line, never per scroll event.
export class SortedIndexSet {
  private readonly items: number[] = [];

  get size(): number {
    return this.items.length;
  }

  // Index of the first item >= i.
  private lowerBound(i: number): number {
    let lo = 0;
    let hi = this.items.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.items[mid]! < i) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }

  has(i: number): boolean {
    return this.items[this.lowerBound(i)] === i;
  }

  add(i: number): void {
    const at = this.lowerBound(i);
    if (this.items[at] !== i) this.items.splice(at, 0, i);
  }

  delete(i: number): void {
    const at = this.lowerBound(i);
    if (this.items[at] === i) this.items.splice(at, 1);
  }

  last(): number | undefined {
    return this.items[this.items.length - 1];
  }

  clear(): void {
    this.items.length = 0;
  }
}
