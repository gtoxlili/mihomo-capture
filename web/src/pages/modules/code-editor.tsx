import CodeMirror, { EditorView } from "@uiw/react-codemirror"
import { javascript } from "@codemirror/lang-javascript"
import { syntaxHighlighting } from "@codemirror/language"

import { highlight, theme } from "@/components/code-theme"

const extensions = [theme, syntaxHighlighting(highlight), javascript(), EditorView.lineWrapping]

export default function CodeEditor({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <CodeMirror
      value={value}
      onChange={onChange}
      minHeight="10rem"
      maxHeight="60vh"
      theme="none"
      aria-label="脚本代码"
      extensions={extensions}
      basicSetup={{
        lineNumbers: true,
        foldGutter: true,
        highlightActiveLine: false,
        highlightActiveLineGutter: false,
        autocompletion: false,
        bracketMatching: true,
        closeBrackets: true,
        indentOnInput: true,
        tabSize: 2,
      }}
    />
  )
}
