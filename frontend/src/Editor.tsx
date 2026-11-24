import { useEffect, useRef } from "react";
import CodeMirror from "codemirror";
import "codemirror/lib/codemirror.css";
import "codemirror/addon/hint/show-hint.css";
import "codemirror/mode/sql/sql";
import "codemirror/addon/hint/show-hint";
import "codemirror/addon/hint/sql-hint";
import "codemirror/addon/edit/matchbrackets";
import "codemirror/addon/edit/closebrackets";
import "codemirror/addon/comment/comment";
import "codemirror/addon/selection/active-line";
import "codemirror/addon/display/placeholder";
import type { Program, Table } from "./model";

export function Editor({
  value,
  onChange,
  onRun,
  driver,
  tables,
  defaultSchema,
  programs,
  errorSql,
}: {
  value: string;
  onChange: (text: string) => void;
  onRun: () => void;
  driver: string;
  tables: Table[];
  defaultSchema: string;
  programs: Program[];
  errorSql?: string;
}) {
  const node = useRef<HTMLTextAreaElement>(null);
  const editor = useRef<CodeMirror.EditorFromTextArea | null>(null);
  const callbacks = useRef({ onChange, onRun });
  useEffect(() => {
    callbacks.current = { onChange, onRun };
  }, [onChange, onRun]);
  useEffect(() => {
    if (!node.current) return;
    const instance = CodeMirror.fromTextArea(node.current, {
      mode: driver === "mysql" ? "text/x-mysql" : "text/x-pgsql",
      theme: "sulcata",
      lineNumbers: true,
      matchBrackets: true,
      autoCloseBrackets: true,
      styleActiveLine: true,
      extraKeys: {
        "Ctrl-Enter": () => callbacks.current.onRun(),
        "Cmd-Enter": () => callbacks.current.onRun(),
        "Ctrl-Space": "autocomplete",
        "Ctrl-/": "toggleComment",
      },
    });
    editor.current = instance;
    instance.on("change", (cm) => callbacks.current.onChange(cm.getValue()));
    return () => {
      instance.toTextArea();
      editor.current = null;
    };
  }, [driver]);
  useEffect(() => {
    if (editor.current && editor.current.getValue() !== value)
      editor.current.setValue(value);
  }, [value]);
  useEffect(() => {
    const hints = Object.fromEntries(
      tables.flatMap((table) => [
        [
          `${table.schema}.${table.name}`,
          table.columns.map((column) => column.name),
        ],
        ...(table.schema === defaultSchema
          ? [[table.name, table.columns.map((column) => column.name)]]
          : []),
      ]),
    );
    editor.current?.setOption("hintOptions", {
      tables: hints,
      completeSingle: false,
      hint: async (
        cm: CodeMirror.Editor,
        options: CodeMirror.ShowHintOptions,
      ) => {
        const base = await CodeMirror.hint.sql(cm, options);
        const cursor = cm.getCursor();
        const token = cm.getTokenAt(cursor);
        if (!/^[\w$]+$/.test(token.string)) return base;
        const result = base || {
          from: CodeMirror.Pos(cursor.line, token.start),
          to: cursor,
          list: [],
        };
        const routines = programs
          .filter((program) => ["function", "procedure"].includes(program.kind))
          .flatMap((program) => [
            `${program.schema}.${program.name}`,
            ...(program.schema === defaultSchema ? [program.name] : []),
          ])
          .filter((name) =>
            name.toLowerCase().startsWith(token.string.toLowerCase()),
          );
        return {
          ...result,
          list: [
            ...result.list,
            ...routines.map((name) => ({
              text: name,
              displayText: `${name}()`,
              className: "CodeMirror-hint-routine",
            })),
          ],
        };
      },
    });
  }, [tables, defaultSchema, programs]);
  useEffect(() => {
    const cm = editor.current;
    if (!cm || !errorSql) return;
    const start = value.indexOf(errorSql);
    if (start < 0) return;
    const marker = cm.markText(
      cm.posFromIndex(start),
      cm.posFromIndex(start + errorSql.length),
      { className: "cm-sql-error" },
    );
    return () => marker.clear();
  }, [errorSql, value]);
  return <textarea ref={node} aria-label="Requête SQL" defaultValue={value} />;
}
