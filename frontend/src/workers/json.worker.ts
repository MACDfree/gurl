// JSON 格式化放在 Worker 里：主线程上对 5MB 正文做 parse + stringify 会卡 100~400ms，
// 而那正是「大响应会不会卡住页面」的瓶颈所在（CodeMirror 只渲染视口，DOM 不是瓶颈）。

interface Req {
  id: number;
  text: string;
}
interface Res {
  id: number;
  text: string;
  /** 是不是合法 JSON。前端据此决定要不要上 JSON 语法高亮——比猜 Content-Type 准。 */
  isJson: boolean;
}

self.onmessage = (e: MessageEvent<Req>) => {
  const { id, text } = e.data;
  try {
    const parsed = JSON.parse(text);
    const out: Res = { id, text: JSON.stringify(parsed, null, 2), isJson: true };
    self.postMessage(out);
  } catch {
    // 解析失败就原样返回：和以前在主线程 catch 里 return 原文的语义一致。
    const out: Res = { id, text, isJson: false };
    self.postMessage(out);
  }
};
