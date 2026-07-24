import React, { useState, useCallback } from 'react'
import { Input, Select, Button, Space, Tag, Tooltip } from 'antd'
import { PlusOutlined, CloseOutlined, InfoCircleOutlined } from '@ant-design/icons'
import type { FieldFilter } from '../services/api'

const OPERATORS = [
  { label: '=', value: 'eq' },
  { label: '!=', value: 'neq' },
  { label: '>', value: 'gt' },
  { label: '<', value: 'lt' },
  { label: '>=', value: 'gte' },
  { label: '<=', value: 'lte' },
  { label: 'contains', value: 'contains' },
  { label: '!contains', value: 'not_contains' },
  { label: 'starts with', value: 'starts_with' },
  { label: 'ends with', value: 'ends_with' },
  { label: 'regex', value: 'regex' },
  { label: 'in', value: 'in' },
  { label: 'not in', value: 'not_in' },
  { label: 'empty', value: 'is_empty' },
  { label: 'not empty', value: 'is_not_empty' },
]

interface DashboardFieldFiltersProps {
  availableFields: string[]
  value: FieldFilter[]
  onChange: (filters: FieldFilter[]) => void
}

export default function DashboardFieldFilters({ availableFields, value, onChange }: DashboardFieldFiltersProps) {
  const [adding, setAdding] = useState(false)

  const addFilter = useCallback(() => {
    if (availableFields.length === 0) return
    onChange([...value, { field: availableFields[0], operator: 'eq', value: '' }])
    setAdding(false)
  }, [availableFields, value, onChange])

  const removeFilter = useCallback((idx: number) => {
    onChange(value.filter((_, i) => i !== idx))
  }, [value, onChange])

  const updateFilter = useCallback((idx: number, key: keyof FieldFilter, val: string) => {
    const next = [...value]
    next[idx] = { ...next[idx], [key]: val }
    onChange(next)
  }, [value, onChange])

  if (availableFields.length === 0) {
    return (
      <div>
        <Space align="start">
          <strong>Field Filters</strong>
          <Tooltip title="No parseable fields available. Add parsers or enable existing ones to expose filterable fields.">
            <InfoCircleOutlined />
          </Tooltip>
        </Space>
        <Input disabled placeholder="No fields available" style={{ marginTop: 8 }} />
      </div>
    )
  }

  return (
    <div>
      <Space align="start" style={{ marginBottom: 8 }}>
        <strong>Field Filters</strong>
        <Tooltip title="Filter logs by parsed fields (e.g. src_ip, action, policy_id). Fields come from enabled log parsers.">
          <InfoCircleOutlined />
        </Tooltip>
        {adding ? (
          <>
            <Select
              style={{ width: 160 }}
              options={availableFields.map(f => ({ label: f, value: f }))}
              placeholder="Pick field"
              onChange={(f) => {
                onChange([...value, { field: f, operator: 'eq', value: '' }])
                setAdding(false)
              }}
              autoFocus
            />
            <Button size="small" onClick={() => setAdding(false)}>Cancel</Button>
          </>
        ) : (
          <Button size="small" icon={<PlusOutlined />} onClick={() => setAdding(true)}>Add Filter</Button>
        )}
      </Space>
      {value.map((ff, idx) => (
        <Space key={idx} style={{ display: 'flex', marginBottom: 4, alignItems: 'center' }} wrap>
          <Tag closable onClose={() => removeFilter(idx)} style={{ marginRight: 0 }}>{ff.field}</Tag>
          <Select
            size="small"
            style={{ width: 120 }}
            value={ff.operator}
            options={OPERATORS}
            onChange={(op) => updateFilter(idx, 'operator', op)}
          />
          {!['is_empty', 'is_not_empty'].includes(ff.operator) && (
            <Input
              size="small"
              style={{ width: 200 }}
              placeholder="value"
              value={ff.value}
              onChange={(e) => updateFilter(idx, 'value', e.target.value)}
            />
          )}
          <Button type="link" danger size="small" icon={<CloseOutlined />} onClick={() => removeFilter(idx)} />
        </Space>
      ))}
    </div>
  )
}