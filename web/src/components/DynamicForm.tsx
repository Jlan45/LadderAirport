import { Checkbox, Input, InputNumber, Select } from 'tdesign-react'
import type { Field } from '../api/client'

export interface DynamicFormProps {
  fields: Field[]
  value: Record<string, unknown>
  onChange: (next: Record<string, unknown>) => void
}

/**
 * Renders form inputs from protocol template fields (string|int|bool|select|password).
 *
 * Important: do NOT wrap controlled inputs in tdesign Form.FormItem here.
 * FormItem injects its own formValue into children and overwrites our value prop,
 * which clears template defaults (listen=0.0.0.0, method, tls_mode, etc.).
 */
export default function DynamicForm({ fields, value, onChange }: DynamicFormProps) {
  function setField(name: string, v: unknown) {
    onChange({ ...value, [name]: v })
  }

  return (
    <div className="la-field-grid">
      {fields.map((f) => {
        // Prefer live params; fall back to template default for display.
        const current =
          value[f.name] !== undefined && value[f.name] !== null
            ? value[f.name]
            : f.default !== undefined
              ? f.default
              : f.type === 'bool'
                ? false
                : ''
        return (
          <div className="la-field" key={f.name}>
            <div className="la-field-label">
              {f.label}
              {f.required ? <span className="la-field-req"> *</span> : null}
            </div>
            {f.description ? <div className="la-field-help">{f.description}</div> : null}
            <div className="la-field-control">{renderInput(f, current, setField)}</div>
          </div>
        )
      })}
    </div>
  )
}

function renderInput(
  f: Field,
  current: unknown,
  setField: (name: string, v: unknown) => void,
) {
  switch (f.type) {
    case 'bool':
      return (
        <Checkbox checked={Boolean(current)} onChange={(checked) => setField(f.name, checked)}>
          启用
        </Checkbox>
      )
    case 'int':
      return (
        <InputNumber
          theme="normal"
          style={{ width: '100%' }}
          value={current === '' || current == null ? undefined : Number(current)}
          onChange={(v) => setField(f.name, v === undefined || v === null ? '' : v)}
          placeholder={f.default != null && f.default !== '' ? `默认 ${String(f.default)}` : undefined}
        />
      )
    case 'select':
      return (
        <Select
          value={current == null ? '' : String(current)}
          onChange={(v) => setField(f.name, v)}
          options={(f.options ?? []).map((opt) => ({
            label: opt === '' ? '(默认 / 不限制)' : opt,
            value: opt,
          }))}
          clearable={!f.required}
          placeholder="请选择"
        />
      )
    case 'password':
      return (
        <Input
          type="password"
          autocomplete="new-password"
          value={current == null ? '' : String(current)}
          onChange={(v) => setField(f.name, v)}
          clearable
          placeholder="留空则服务端自动生成"
        />
      )
    case 'string':
    default:
      return (
        <Input
          value={current == null ? '' : String(current)}
          onChange={(v) => setField(f.name, v)}
          clearable
          placeholder={
            f.default != null && f.default !== ''
              ? `默认 ${String(f.default)}`
              : undefined
          }
        />
      )
  }
}

/** Build initial params object from template field defaults. */
export function defaultsFromFields(fields: Field[]): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const f of fields) {
    if (f.default !== undefined) {
      out[f.name] = f.default
    } else if (f.type === 'bool') {
      out[f.name] = false
    } else if (f.type === 'int') {
      out[f.name] = ''
    } else {
      out[f.name] = ''
    }
  }
  return out
}
