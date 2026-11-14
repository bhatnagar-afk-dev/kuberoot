// The contract every code editor implementation follows. The rest of the app
// uses CodeEditor.svelte and these types only, never an editor library.

export type CodeLanguage = 'yaml' | 'text';

export interface CodeEditorProps {
  value: string;
  language: CodeLanguage;
  readOnly: boolean;
  onChange?: (value: string) => void;
}
