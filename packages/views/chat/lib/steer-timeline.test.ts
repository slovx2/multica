// @vitest-environment node
import { describe, expect, it } from "vitest";
import { spliceSteeredMessages, type SteerAnchor } from "./steer-timeline";

interface Row {
  id: string;
  taskRow?: string;
  steer?: { taskId: string; afterSeq: number };
}

const describeRow = (row: Row): SteerAnchor => ({
  taskRow: row.taskRow ?? null,
  steer: row.steer ?? null,
});

function shape(rows: Row[]) {
  return spliceSteeredMessages(rows, describeRow).map((entry) =>
    entry.kind === "segment"
      ? `segment:${entry.taskId}:${entry.afterSeq ?? "-"}..${entry.upToSeq}`
      : `${entry.item.id}${entry.afterSeq === undefined ? "" : `@${entry.afterSeq}`}`,
  );
}

describe("spliceSteeredMessages", () => {
  it("leaves rows untouched without steered messages", () => {
    expect(shape([{ id: "u1" }, { id: "a1", taskRow: "t1" }])).toEqual(["u1", "a1"]);
  });

  it("cuts the receiving turn at the delivery point", () => {
    expect(
      shape([
        { id: "u1" },
        { id: "steer", steer: { taskId: "t1", afterSeq: 4 } },
        { id: "a1", taskRow: "t1" },
      ]),
    ).toEqual(["u1", "segment:t1:-..4", "steer", "a1@4"]);
  });

  it("orders several steers by delivery point", () => {
    expect(
      shape([
        { id: "u1" },
        { id: "late", steer: { taskId: "t1", afterSeq: 9 } },
        { id: "early", steer: { taskId: "t1", afterSeq: 3 } },
        { id: "a1", taskRow: "t1" },
      ]),
    ).toEqual(["u1", "segment:t1:-..3", "early", "segment:t1:3..9", "late", "a1@9"]);
  });

  it("keeps a steer in place when its turn has no rendered row", () => {
    expect(
      shape([
        { id: "u1" },
        { id: "steer", steer: { taskId: "t2", afterSeq: 1 } },
        { id: "a1", taskRow: "t1" },
      ]),
    ).toEqual(["u1", "steer", "a1"]);
  });
});
