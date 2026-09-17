const CLASS: Record<string, string> = {
  GET: "m-get",
  POST: "m-post",
  PUT: "m-put",
  DELETE: "m-delete",
  PATCH: "m-patch",
};

export function methodClass(m?: string) {
  return CLASS[(m ?? "").toUpperCase()] ?? "m-other";
}
