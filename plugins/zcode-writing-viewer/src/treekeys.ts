/**
 * Pure keyboard-navigation logic for the unit tree.
 *
 * The panel's tree renders as one flat pre-order list of nodes (see
 * ./overview's unitTree); this module decides what an arrow key pressed on
 * one node should do — move the selection, or collapse/expand a subtree —
 * without touching React or the DOM. The client entry applies the decision
 * (and moves DOM focus), so the behavior stays unit-testable as pure data:
 *
 * - ↑/↓ move the selection to the previous/next *visible* node and the
 *   detail area follows the selection;
 * - → expands a collapsed node with children, or moves to its first child
 *   when it is already expanded;
 * - ← collapses an expanded node with children, or moves to its parent;
 * - anything else, including arrows on a single-node tree, is `none` and the
 *   key keeps its default browser behavior.
 *
 * Collapsed state is a plain `Record<ref, boolean>` (true = collapsed); an
 * empty record shows the whole tree, which is the default the unit-tree
 * navigation requirement describes.
 */
import type { TreeNode } from './overview';

/** Which nodes of the full pre-order tree are currently shown. */
export function visibleTreeNodes(nodes: readonly TreeNode[], collapsed: Record<string, boolean>): TreeNode[] {
  const out: TreeNode[] = [];
  let hiddenUnderDepth = -1;
  for (const node of nodes) {
    if (hiddenUnderDepth >= 0) {
      if (node.depth > hiddenUnderDepth) continue;
      hiddenUnderDepth = -1;
    }
    out.push(node);
    if (collapsed[node.ref] === true) hiddenUnderDepth = node.depth;
  }
  return out;
}

/** Whether the node at `index` in the full pre-order list has children. */
function hasChildrenAt(nodes: readonly TreeNode[], index: number): boolean {
  const node = nodes[index];
  const next = nodes[index + 1];
  return node !== undefined && next !== undefined && next.depth > node.depth;
}

/** The ref of the node parenting `index`, or null for a top-level node. */
function parentRefAt(nodes: readonly TreeNode[], index: number): string | null {
  const node = nodes[index];
  if (node === undefined) return null;
  for (let i = index - 1; i >= 0; i -= 1) {
    const ancestor = nodes[i];
    if (ancestor !== undefined && ancestor.depth < node.depth) return ancestor.ref;
  }
  return null;
}

/** What one key pressed on `currentRef` should do; `kind: 'none'` = default. */
export type TreeKeyAction =
  | { kind: 'none' }
  | { kind: 'select'; ref: string }
  | { kind: 'toggle'; ref: string; collapsed: boolean };

export function treeKeyAction(
  nodes: readonly TreeNode[],
  collapsed: Record<string, boolean>,
  currentRef: string,
  key: string,
): TreeKeyAction {
  const fullIndex = nodes.findIndex((node) => node.ref === currentRef);
  if (fullIndex === -1) return { kind: 'none' };
  const visible = visibleTreeNodes(nodes, collapsed);
  const visibleIndex = visible.findIndex((node) => node.ref === currentRef);
  const children = hasChildrenAt(nodes, fullIndex);
  switch (key) {
    case 'ArrowDown':
    case 'ArrowUp': {
      // A selection hidden by an ancestor's collapse re-enters at the first
      // visible node instead of getting stuck.
      if (visibleIndex === -1) {
        const first = visible[0];
        return first === undefined ? { kind: 'none' } : { kind: 'select', ref: first.ref };
      }
      const step = key === 'ArrowDown' ? 1 : -1;
      const target = visible[visibleIndex + step];
      return target === undefined ? { kind: 'none' } : { kind: 'select', ref: target.ref };
    }
    case 'ArrowRight': {
      if (!children) return { kind: 'none' };
      if (collapsed[currentRef] === true) return { kind: 'toggle', ref: currentRef, collapsed: false };
      const child = nodes[fullIndex + 1];
      return child === undefined ? { kind: 'none' } : { kind: 'select', ref: child.ref };
    }
    case 'ArrowLeft': {
      if (children && collapsed[currentRef] !== true) return { kind: 'toggle', ref: currentRef, collapsed: true };
      const parent = parentRefAt(nodes, fullIndex);
      return parent === null ? { kind: 'none' } : { kind: 'select', ref: parent };
    }
    default:
      return { kind: 'none' };
  }
}
