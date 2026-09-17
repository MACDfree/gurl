import type { Ref } from "vue";

export interface KV {
  key: string;
  value: string;
  enabled: boolean;
}

export interface ColApi {
  open(node: any): void;
  remove(id: string): void;
  addFolder(id: string): void;
  startRename(id: string): void;
  rename(id: string, name: string): void;
  editingId: Ref<string | null>;
}
