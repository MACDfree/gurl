import type { TreeOption } from "naive-ui";
import type { CollectionNode } from "./types";

export type DropPos = "before" | "inside" | "after";

/** CollectionNode → NTree 的数据。节点本身留在原树上改，这里只做视图映射。 */
export function toTreeOptions(nodes: CollectionNode[]): TreeOption[] {
  return nodes
    .filter((n): n is CollectionNode => !!n)
    .map((n) => ({
      key: n.id,
      label: n.name,
      // 自定义字段，renderLabel / renderSuffix 里读
      nodeKind: n.type,
      request: n.request ?? null,
      children: n.children?.length ? toTreeOptions(n.children.filter((c): c is CollectionNode => !!c)) : undefined,
    }));
}

export function findNode(nodes: CollectionNode[], id: string): CollectionNode | null {
  for (const n of nodes.filter((x): x is CollectionNode => !!x)) {
    if (n.id === id) return n;
    const hit = findNode((n.children ?? []).filter((c): c is CollectionNode => !!c), id);
    if (hit) return hit;
  }
  return null;
}

function parentListOf(nodes: CollectionNode[], id: string): CollectionNode[] | null {
  for (const n of nodes.filter(Boolean)) {
    const kids = (n.children ??= []) as CollectionNode[];
    if (kids.some((c) => c?.id === id)) return kids;
    const hit = parentListOf(kids, id);
    if (hit) return hit;
  }
  return null;
}

export function removeFromTree(roots: CollectionNode[], id: string): boolean {
  const top = roots.findIndex((n) => n?.id === id);
  if (top >= 0) {
    roots.splice(top, 1);
    return true;
  }
  const list = parentListOf(roots, id);
  if (!list) return false;
  const i = list.findIndex((n) => n?.id === id);
  if (i < 0) return false;
  list.splice(i, 1);
  return true;
}

/** target 是否位于 drag 的子树里——是就会形成环，必须拒绝。 */
export function contains(drag: CollectionNode, targetId: string): boolean {
  if (drag.id === targetId) return true;
  return (drag.children ?? []).some((c) => c && contains(c, targetId));
}

/**
 * 拖拽落位。先摘出被拖节点，再按目标当前位置算插入点——同层移动时如果先算索引，
 * splice 会让它漂移一格。
 */
export function moveNode(
  roots: CollectionNode[],
  dragId: string,
  targetId: string,
  pos: DropPos,
): void {
  if (dragId === targetId) return;
  const drag = findNode(roots, dragId);
  const target = findNode(roots, targetId);
  if (!drag || !target || contains(drag, targetId)) return;

  const dragList = parentListOf(roots, dragId) ?? roots;
  const at = dragList.indexOf(drag);
  if (at < 0) return;
  dragList.splice(at, 1);

  if (pos === "inside" && target.type !== "request") {
    (target.children ??= []).push(drag);
    return;
  }
  const list = parentListOf(roots, targetId) ?? roots;
  const i = Math.max(0, list.indexOf(target));
  list.splice(pos === "before" ? i : i + 1, 0, drag);
}
