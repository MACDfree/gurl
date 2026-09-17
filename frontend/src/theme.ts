import type { GlobalThemeOverrides } from "naive-ui";

/**
 * Naive UI 是 CSS-in-JS，不读 CSS 变量，所以这套值和 styles/base.css 的 :root token
 * 之间没有桥接机制——改一处要记得改另一处。需要同步的：
 *   primary #0a7cff / hover #4c9fff / pressed #0062d6
 *   success #248a3d / warning #b25000 / error #d70015
 *   文字 #1d1d1f · #48484d · #86868b，边框 rgba(0,0,0,.16)，分隔线 rgba(0,0,0,.09)
 *   圆角 6px，字号 13px，控件高度 24px
 *
 * 组件级覆盖（Select/Modal/Tree 的 peers）等真正换上那个组件时再加：
 * peers 里的变量名写错 TS 不报错、静默失效，只能对着装好的源码核并目视验证。
 */
export const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: "#0A7CFF",
    primaryColorHover: "#4C9FFF",
    primaryColorPressed: "#0062D6",
    primaryColorSuppl: "#0A7CFF",

    infoColor: "#0A60D6",
    successColor: "#248A3D",
    warningColor: "#B25000",
    errorColor: "#D70015",

    borderRadius: "6px",
    borderRadiusSmall: "5px",

    fontSize: "13px",
    fontSizeSmall: "12px",
    fontSizeTiny: "11px",
    heightSmall: "24px",
    heightMedium: "28px",

    textColorBase: "#1D1D1F",
    textColor1: "#1D1D1F",
    textColor2: "#48484D",
    textColor3: "#86868B",
    placeholderColor: "#86868B",

    borderColor: "rgba(0,0,0,0.16)",
    dividerColor: "rgba(0,0,0,0.09)",

    bodyColor: "#F0F0F4",
    cardColor: "#FFFFFF",
  },
};
