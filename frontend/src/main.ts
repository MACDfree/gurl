import { createApp, h } from "vue";
import { NConfigProvider, NDialogProvider, NMessageProvider } from "naive-ui";
import App from "./App.vue";
import { themeOverrides } from "./theme";
import "./styles/base.css";

// NConfigProvider 必须包在最外层，否则 useMessage 弹出的 toast 不带主题。
createApp({
  render: () =>
    h(NConfigProvider, { themeOverrides }, {
      default: () =>
        h(NMessageProvider, null, {
          default: () => h(NDialogProvider, null, { default: () => h(App) }),
        }),
    }),
}).mount("#app");
