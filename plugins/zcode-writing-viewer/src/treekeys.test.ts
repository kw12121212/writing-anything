import { describe, expect, it } from 'vitest';
import { treeKeyAction, visibleTreeNodes } from './treekeys';
import type { TreeNode } from './overview';

/** A three-level tree mirroring the novel fixture: work → part → ch → scenes. */
function novelTree(): TreeNode[] {
  const node = (ref: string, depth: number): TreeNode => ({
    ref, label: ref, type: null, stage: null, depth, aggregated: false, progress: null, blockerCount: 0,
  });
  return [
    node('my-novel', 0),
    node('my-novel/part-1', 1),
    node('my-novel/part-1/ch-1', 2),
    node('my-novel/part-1/ch-1/scene-1', 3),
    node('my-novel/part-1/ch-1/scene-2', 3),
    node('my-novel/part-2', 1),
    node('my-novel/part-2/ch-2', 2),
  ];
}

describe('visibleTreeNodes', () => {
  it('shows the whole tree when nothing is collapsed', () => {
    expect(visibleTreeNodes(novelTree(), {}).map((n) => n.ref)).toEqual([
      'my-novel', 'my-novel/part-1', 'my-novel/part-1/ch-1', 'my-novel/part-1/ch-1/scene-1',
      'my-novel/part-1/ch-1/scene-2', 'my-novel/part-2', 'my-novel/part-2/ch-2',
    ]);
  });

  it('hides the whole subtree under a collapsed node and keeps later siblings', () => {
    const visible = visibleTreeNodes(novelTree(), { 'my-novel/part-1': true }).map((n) => n.ref);
    expect(visible).toEqual(['my-novel', 'my-novel/part-1', 'my-novel/part-2', 'my-novel/part-2/ch-2']);
  });

  it('hides nested collapses without affecting earlier siblings', () => {
    const visible = visibleTreeNodes(novelTree(), { 'my-novel/part-1/ch-1': true }).map((n) => n.ref);
    expect(visible).toEqual(['my-novel', 'my-novel/part-1', 'my-novel/part-1/ch-1', 'my-novel/part-2', 'my-novel/part-2/ch-2']);
  });
});

describe('treeKeyAction', () => {
  const tree = novelTree();

  it('ArrowDown moves to the next visible node and stops at the end', () => {
    expect(treeKeyAction(tree, {}, 'my-novel', 'ArrowDown')).toEqual({ kind: 'select', ref: 'my-novel/part-1' });
    expect(treeKeyAction(tree, {}, 'my-novel/part-2/ch-2', 'ArrowDown')).toEqual({ kind: 'none' });
  });

  it('ArrowUp moves to the previous visible node and stops at the top', () => {
    expect(treeKeyAction(tree, {}, 'my-novel/part-1/ch-1/scene-1', 'ArrowUp')).toEqual({ kind: 'select', ref: 'my-novel/part-1/ch-1' });
    expect(treeKeyAction(tree, {}, 'my-novel', 'ArrowUp')).toEqual({ kind: 'none' });
  });

  it('vertical arrows skip the subtree hidden under a collapsed ancestor', () => {
    const collapsed = { 'my-novel/part-1': true };
    expect(treeKeyAction(tree, collapsed, 'my-novel/part-1', 'ArrowDown')).toEqual({ kind: 'select', ref: 'my-novel/part-2' });
    expect(treeKeyAction(tree, collapsed, 'my-novel/part-2', 'ArrowUp')).toEqual({ kind: 'select', ref: 'my-novel/part-1' });
  });

  it('a selection hidden by a collapse re-enters at the first visible node', () => {
    expect(treeKeyAction(tree, { 'my-novel/part-1': true }, 'my-novel/part-1/ch-1/scene-1', 'ArrowDown'))
      .toEqual({ kind: 'select', ref: 'my-novel' });
  });

  it('ArrowRight expands a collapsed parent and descends an expanded one', () => {
    expect(treeKeyAction(tree, { 'my-novel/part-1': true }, 'my-novel/part-1', 'ArrowRight'))
      .toEqual({ kind: 'toggle', ref: 'my-novel/part-1', collapsed: false });
    expect(treeKeyAction(tree, {}, 'my-novel/part-1', 'ArrowRight')).toEqual({ kind: 'select', ref: 'my-novel/part-1/ch-1' });
    expect(treeKeyAction(tree, {}, 'my-novel/part-1/ch-1/scene-1', 'ArrowRight')).toEqual({ kind: 'none' });
  });

  it('ArrowLeft collapses an expanded parent and climbs from a leaf', () => {
    expect(treeKeyAction(tree, {}, 'my-novel/part-1', 'ArrowLeft')).toEqual({ kind: 'toggle', ref: 'my-novel/part-1', collapsed: true });
    expect(treeKeyAction(tree, {}, 'my-novel/part-1/ch-1/scene-1', 'ArrowLeft')).toEqual({ kind: 'select', ref: 'my-novel/part-1/ch-1' });
    // An expanded root also collapses; there is simply no parent to climb to.
    expect(treeKeyAction(tree, {}, 'my-novel', 'ArrowLeft')).toEqual({ kind: 'toggle', ref: 'my-novel', collapsed: true });
    expect(treeKeyAction(tree, { 'my-novel': true }, 'my-novel', 'ArrowLeft')).toEqual({ kind: 'none' });
  });

  it('leaves other keys and unknown refs to the browser', () => {
    expect(treeKeyAction(tree, {}, 'my-novel', 'Enter')).toEqual({ kind: 'none' });
    expect(treeKeyAction(tree, {}, 'my-novel', 'Tab')).toEqual({ kind: 'none' });
    expect(treeKeyAction(tree, {}, 'no-such-ref', 'ArrowDown')).toEqual({ kind: 'none' });
  });
});
