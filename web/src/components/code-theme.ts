import { EditorView } from "@uiw/react-codemirror"
import { HighlightStyle } from "@codemirror/language"
import { tags as t } from "@lezer/highlight"

// Keys take the signal teal, values the status hues, structure recedes to grey.
export const highlight = HighlightStyle.define([
  { tag: [t.propertyName, t.attributeName], color: "#0e7490" },
  { tag: [t.string, t.special(t.string)], color: "#2f6f4f" },
  { tag: [t.number, t.bool, t.null, t.atom], color: "#946200" },
  { tag: [t.keyword, t.tagName, t.typeName], color: "#6941c6" },
  { tag: [t.comment], color: "#98a2b3", fontStyle: "italic" },
  { tag: [t.punctuation, t.bracket, t.separator], color: "#98a2b3" },
])

export const theme = EditorView.theme({
  "&": {
    fontSize: "12px",
    color: "var(--ink)",
    backgroundColor: "#fbfcfd",
    borderRadius: "var(--radius)",
    border: "1px solid var(--line)",
  },
  "&.cm-focused": { outline: "2px solid color-mix(in oklab, var(--signal) 40%, transparent)" },
  ".cm-scroller": { fontFamily: "var(--font-mono)", lineHeight: "1.6" },
  ".cm-gutters": { backgroundColor: "transparent", border: "none", color: "#c0c6d0" },
  ".cm-activeLine, .cm-activeLineGutter": { backgroundColor: "transparent" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": {
    backgroundColor: "color-mix(in oklab, var(--signal) 18%, transparent) !important",
  },
  ".cm-foldPlaceholder": { backgroundColor: "var(--muted)", border: "none", color: "var(--muted-foreground)" },
  ".cm-panels": { backgroundColor: "var(--card)", borderColor: "var(--border)" },
})
