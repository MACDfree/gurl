<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Compartment, EditorState } from "@codemirror/state";
import { EditorView, keymap, lineNumbers as cmLineNumbers, placeholder as cmPlaceholder } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { bracketMatching, HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { json } from "@codemirror/lang-json";
import { tags } from "@lezer/highlight";

const props = withDefaults(
  defineProps<{
    modelValue: string;
    readonly?: boolean;
    /** 只有确认是 JSON 才上语法高亮：把 HTML 当日志标成 JSON 比不标高亮更糟 */
    json?: boolean;
    wrap?: boolean;
    lineNumbers?: boolean;
    placeholder?: string;
  }>(),
  { readonly: false, json: false, wrap: true, lineNumbers: false, placeholder: "" },
);

const emit = defineEmits<{ (e: "update:modelValue", v: string): void }>();

const host = ref<HTMLElement | null>(null);
let view: EditorView | null = null;

// 配色直接映射到全局 token，不再引入第四套颜色系统。
const highlight = HighlightStyle.define([
  { tag: tags.propertyName, color: "var(--accent)" },
  { tag: tags.string, color: "var(--ok)" },
  { tag: tags.number, color: "var(--warn)" },
  { tag: [tags.bool, tags.null], color: "var(--fg-secondary)" },
  { tag: tags.punctuation, color: "var(--muted)" },
]);

const theme = EditorView.theme({
  "&": { height: "100%", fontSize: "12px", backgroundColor: "transparent" },
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": { fontFamily: "ui-monospace, 'SF Mono', Menlo, monospace", lineHeight: "1.55" },
  ".cm-gutters": { backgroundColor: "transparent", border: "none", color: "var(--muted)" },
  ".cm-activeLine": { backgroundColor: "transparent" },
  ".cm-activeLineGutter": { backgroundColor: "transparent" },
  ".cm-cursor": { borderLeftColor: "var(--accent)" },
  ".cm-selectionBackground": { backgroundColor: "rgba(10, 124, 255, 0.18)" },
});

const roC = new Compartment();
const wrapC = new Compartment();
const lnC = new Compartment();
const langC = new Compartment();

const roOf = (v: boolean) => [EditorState.readOnly.of(v), EditorView.editable.of(!v)];
// 只读要同时设这两项：光 readOnly 还会显示光标、并保留拖拽选中的编辑语义，
// 而 editable(false) 才真正把输入能力拿掉。
const roExt = () => roOf(props.readonly);
const wrapExt = () => (props.wrap ? [EditorView.lineWrapping] : []);
const lnExt = () => (props.lineNumbers ? [cmLineNumbers()] : []);
const langExt = () => (props.json ? [json()] : []);

onMounted(() => {
  if (!host.value) return;
  view = new EditorView({
    parent: host.value,
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        theme,
        syntaxHighlighting(highlight),
        bracketMatching(),
        history(),
        keymap.of([...defaultKeymap, ...historyKeymap]),
        props.placeholder ? cmPlaceholder(props.placeholder) : [],
        EditorView.updateListener.of((u) => {
          if (u.docChanged) emit("update:modelValue", u.state.doc.toString());
        }),
        roC.of(roExt()),
        wrapC.of(wrapExt()),
        lnC.of(lnExt()),
        langC.of(langExt()),
      ],
    }),
  });
});

onBeforeUnmount(() => view?.destroy());

function reconfigure() {
  view?.dispatch({
    effects: [roC.reconfigure(roExt()), wrapC.reconfigure(wrapExt()), lnC.reconfigure(lnExt()), langC.reconfigure(langExt())],
  });
}

watch(() => [props.readonly, props.json, props.wrap, props.lineNumbers], reconfigure);

watch(
  () => props.modelValue,
  (v) => {
    if (!view || view.state.doc.toString() === v) return; // 自己 emit 回来的那次直接跳过
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: v } });
  },
);
</script>

<template>
  <div ref="host" class="cm-host" />
</template>

<style scoped>
.cm-host {
  height: 100%;
  min-height: 0;
  overflow: hidden;
}
</style>
