export interface Result {
  text: string;
  isJson: boolean;
}

let worker: Worker | null = null;
let seq = 0;
const pending = new Map<number, (r: Result) => void>();

// 只留最后一次结果：同一个响应来回切 Pretty 是最常见的重复劳动。
let lastKey = "";
let lastResult: Result | null = null;

/** 在 Worker 里格式化，主线程不碰 parse/stringify。 */
export function formatBody(key: string, text: string): Promise<Result> {
  if (lastKey === key && lastResult) return Promise.resolve(lastResult);
  const w = ensureWorker();
  const id = ++seq;
  return new Promise<Result>((resolve) => {
    pending.set(id, (r) => {
      lastKey = key;
      lastResult = r;
      resolve(r);
    });
    w.postMessage({ id, text });
  });
}

function ensureWorker(): Worker {
  if (!worker) {
    worker = new Worker(new URL("./workers/json.worker.ts", import.meta.url), { type: "module" });
    worker.onmessage = (e: MessageEvent<Result & { id: number }>) => {
      const { id, ...result } = e.data;
      const done = pending.get(id);
      pending.delete(id);
      done?.(result); // 没有对应的 pending 就是迟到的结果，丢弃
    };
  }
  return worker;
}
