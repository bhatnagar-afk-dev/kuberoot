// CodeMirror 6 setup. This is the only module that imports @codemirror/*;
// CodeMirrorEditor.svelte loads it lazily so it stays out of the startup
// bundle.
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import { yaml } from '@codemirror/lang-yaml';
import { HighlightStyle, codeFolding, foldGutter, foldKeymap, syntaxHighlighting } from '@codemirror/language';
import { highlightSelectionMatches, search, searchKeymap } from '@codemirror/search';
import { Compartment, EditorState, type Extension } from '@codemirror/state';
import { EditorView, drawSelection, highlightActiveLine, keymap, lineNumbers } from '@codemirror/view';
import { tags as t } from '@lezer/highlight';
import type { CodeEditorProps, CodeLanguage } from '../types';

/** A live editor; values passed in replace what it shows. */
export interface Editor {
  setValue(value: string): void;
  setLanguage(language: CodeLanguage): void;
  setReadOnly(readOnly: boolean): void;
  destroy(): void;
}

// Colours come from the app's CSS variables, so light and dark mode follow
// the system like the rest of the UI.
const theme = EditorView.theme({
  '&': {
    height: '100%',
    fontSize: '12px',
    backgroundColor: 'var(--surface)',
    color: 'var(--text)',
  },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': {
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
    lineHeight: '1.5',
  },
  '.cm-content': { caretColor: 'var(--text)' },
  '.cm-gutters': {
    backgroundColor: 'var(--bg)',
    color: 'var(--muted)',
    borderRight: '1px solid var(--border)',
  },
  '.cm-activeLine': { backgroundColor: 'var(--hover)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--hover)' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground': { backgroundColor: 'var(--selected) !important' },
  '.cm-searchMatch': { backgroundColor: 'var(--syn-match)' },
  '.cm-panels': { backgroundColor: 'var(--bg)', color: 'var(--text)' },
  '.cm-panels-bottom': { borderTop: '1px solid var(--border)' },
  '.cm-foldPlaceholder': { backgroundColor: 'var(--hover)', border: 'none', color: 'var(--muted)' },
});

const highlight = HighlightStyle.define([
  { tag: [t.propertyName, t.definition(t.propertyName)], color: 'var(--syn-key)' },
  { tag: [t.string, t.special(t.string), t.content], color: 'var(--syn-string)' },
  { tag: [t.number, t.bool, t.null, t.atom], color: 'var(--syn-literal)' },
  { tag: [t.comment, t.meta, t.punctuation, t.separator], color: 'var(--muted)' },
  { tag: [t.keyword, t.labelName, t.typeName], color: 'var(--syn-keyword)' },
]);

function languageExtension(language: CodeLanguage): Extension {
  return language === 'yaml' ? [yaml(), syntaxHighlighting(highlight)] : [];
}

/** Creates an editor in parent. */
export function createEditor(parent: HTMLElement, props: CodeEditorProps): Editor {
  const language = new Compartment();
  const readOnly = new Compartment();
  const view = new EditorView({
    parent,
    state: EditorState.create({
      doc: props.value,
      extensions: [
        lineNumbers(),
        foldGutter(),
        codeFolding(),
        drawSelection(),
        highlightActiveLine(),
        highlightSelectionMatches(),
        history(),
        search({ top: false }),
        keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap, ...foldKeymap]),
        theme,
        language.of(languageExtension(props.language)),
        readOnly.of(EditorState.readOnly.of(props.readOnly)),
        EditorView.updateListener.of((u) => {
          if (u.docChanged && props.onChange) props.onChange(u.state.doc.toString());
        }),
      ],
    }),
  });

  return {
    setValue(value) {
      const current = view.state.doc.toString();
      if (value === current) return;
      // Replace only what changed, so the scroll position and selection stay
      // put when an object is re-fetched.
      view.dispatch({ changes: minimalChange(current, value) });
    },
    setLanguage(l) {
      view.dispatch({ effects: language.reconfigure(languageExtension(l)) });
    },
    setReadOnly(r) {
      view.dispatch({ effects: readOnly.reconfigure(EditorState.readOnly.of(r)) });
    },
    destroy() {
      view.destroy();
    },
  };
}

/** The single replacement that turns a into b, skipping their common prefix and suffix. */
export function minimalChange(a: string, b: string): { from: number; to: number; insert: string } {
  const max = Math.min(a.length, b.length);
  let start = 0;
  while (start < max && a[start] === b[start]) start++;
  let end = 0;
  while (end < max - start && a[a.length - 1 - end] === b[b.length - 1 - end]) end++;
  return { from: start, to: a.length - end, insert: b.slice(start, b.length - end) };
}
