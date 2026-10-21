/**
 * crypto.randomUUID 只在安全上下文可用。127.0.0.1 算，但一旦有人用 -host 0.0.0.0
 * 起服务再从局域网 IP 打开，它就会抛异常——所以留一条 getRandomValues 的路。
 */
export function uuid(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

/**
 * 复制。navigator.clipboard 只在安全上下文有，用 -host 0.0.0.0 起服务再从局域网 IP
 * 打开就没有了；和 uuid() 同一道坎，留一条 execCommand 的退路。
 */
export async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
    return;
  } catch {
    /* 落到下面的退路 */
  }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  document.execCommand("copy");
  ta.remove();
}
