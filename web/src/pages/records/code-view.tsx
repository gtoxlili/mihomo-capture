import CodeMirror, { EditorView } from "@uiw/react-codemirror"
import { css } from "@codemirror/lang-css"
import { html } from "@codemirror/lang-html"
import { javascript } from "@codemirror/lang-javascript"
import { json } from "@codemirror/lang-json"
import { xml } from "@codemirror/lang-xml"
import { syntaxHighlighting } from "@codemirror/language"

import { highlight, theme } from "@/components/code-theme"
import type { Language } from "@/lib/body"

const languages = { json, html, xml, javascript, css }

export default function CodeView({ text, language }: { text: string; language: Language }) {
  const extensions = [theme, syntaxHighlighting(highlight), EditorView.lineWrapping]
  if (language !== "text") extensions.push(languages[language]())
  return (
    <CodeMirror
      value={text}
      readOnly
      maxHeight="70vh"
      theme="none"
      extensions={extensions}
      basicSetup={{
        lineNumbers: true,
        foldGutter: true,
        highlightActiveLine: false,
        highlightActiveLineGutter: false,
        highlightSelectionMatches: true,
        searchKeymap: true,
        autocompletion: false,
        bracketMatching: true,
      }}
    />
  )
}
