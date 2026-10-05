export interface SteerAnchor {
  /** Task whose process this row renders (assistant or live row); null otherwise. */
  taskRow: string | null;
  /** Set when this row is a user message steered into a running turn. */
  steer: { taskId: string; afterSeq: number } | null;
}

export type SteerTimelineEntry<T> =
  /** `afterSeq` is set on a task row whose earlier process moved into segments. */
  | { kind: "item"; item: T; afterSeq?: number }
  /** Process of `taskId` in (afterSeq, upToSeq], shown before a steered message. */
  | { kind: "segment"; taskId: string; afterSeq?: number; upToSeq: number };

/**
 * Move each steered user message from its queue-time position into the turn
 * that received it: the turn's process is cut at the message's delivery point,
 * so the reader sees process so far → the message → the rest of the turn.
 * A steered message whose turn row is not rendered keeps its own position.
 */
export function spliceSteeredMessages<T>(
  items: readonly T[],
  describe: (item: T) => SteerAnchor,
): SteerTimelineEntry<T>[] {
  const anchors = items.map(describe);
  const taskRows = new Set(
    anchors.map((anchor) => anchor.taskRow).filter((id): id is string => !!id),
  );
  const steersByTask = new Map<string, { item: T; afterSeq: number; index: number }[]>();
  anchors.forEach((anchor, index) => {
    const steer = anchor.steer;
    if (!steer || !taskRows.has(steer.taskId)) return;
    const list = steersByTask.get(steer.taskId) ?? [];
    list.push({ item: items[index]!, afterSeq: steer.afterSeq, index });
    steersByTask.set(steer.taskId, list);
  });
  if (steersByTask.size === 0) return items.map((item) => ({ kind: "item", item }));

  const moved = new Set<number>();
  for (const list of steersByTask.values()) {
    list.sort((a, b) => a.afterSeq - b.afterSeq || a.index - b.index);
    for (const steer of list) moved.add(steer.index);
  }

  const entries: SteerTimelineEntry<T>[] = [];
  items.forEach((item, index) => {
    if (moved.has(index)) return;
    const taskId = anchors[index]!.taskRow;
    const steers = taskId ? steersByTask.get(taskId) : undefined;
    if (!taskId || !steers) {
      entries.push({ kind: "item", item });
      return;
    }
    let afterSeq: number | undefined;
    for (const steer of steers) {
      entries.push({ kind: "segment", taskId, afterSeq, upToSeq: steer.afterSeq });
      entries.push({ kind: "item", item: steer.item });
      afterSeq = steer.afterSeq;
    }
    entries.push({ kind: "item", item, afterSeq });
  });
  return entries;
}
