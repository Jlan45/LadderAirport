import type { Field } from '../api/client'

export interface DynamicFormProps {
  fields: Field[]
  value: Record<string, unknown>
  onChange: (next: Record<string, unknown>) => void
}

/** Renders form inputs from protocol template fields (string|int|bool|select|password). */
export default function DynamicForm({ fields, value, onChange }: DynamicFormProps) {
  function setField(name: string, v: unknown) {
    onChange({ ...value, [name]: v })
  }

  return (
    <div className="dynamic-form">
      {fields.map((f) => {
        const id = `field-${f.name}`
        const current = value[f.name] ?? f.default ?? (f.type === 'bool' ? false : '')
        return (
          <div className="form-row" key={f.name}>
            <label htmlFor={id}>
              {f.label}
              {f.required ? <span className="req"> *</span> : null}
            </label>
            {f.description ? (
              <div className="field-hint">{f.description}</div>
            ) : null}
            {renderInput(f, id, current, setField)}
          </div>
        )
      })}
    </div>
  )
}

function renderInput(
  f: Field,
  id: string,
  current: unknown,
  setField: (name: string, v: unknown) => void,
) {
  switch (f.type) {
    case 'bool':
      return (
        <input
          id={id}
          type="checkbox"
          checked={Boolean(current)}
          onChange={(e) => setField(f.name, e.target.checked)}
        />
      )
    case 'int':
      return (
        <input
          id={id}
          type="number"
          value={current === '' || current == null ? '' : String(current)}
          required={f.required}
          onChange={(e) => {
            const raw = e.target.value
            if (raw === '') {
              setField(f.name, '')
              return
            }
            const n = Number(raw)
            setField(f.name, Number.isFinite(n) ? n : raw)
          }}
        />
      )
    case 'select':
      return (
        <select
          id={id}
          value={current == null ? '' : String(current)}
          required={f.required}
          onChange={(e) => setField(f.name, e.target.value)}
        >
          {(f.options ?? []).map((opt) => (
            <option key={opt === '' ? '__empty' : opt} value={opt}>
              {opt === '' ? '(默认)' : opt}
            </option>
          ))}
        </select>
      )
    case 'password':
      return (
        <input
          id={id}
          type="password"
          autoComplete="new-password"
          value={current == null ? '' : String(current)}
          required={f.required}
          onChange={(e) => setField(f.name, e.target.value)}
        />
      )
    case 'string':
    default:
      return (
        <input
          id={id}
          type="text"
          value={current == null ? '' : String(current)}
          required={f.required}
          onChange={(e) => setField(f.name, e.target.value)}
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
