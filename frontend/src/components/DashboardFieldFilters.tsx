import { useState, useCallback } from 'react'
import { Input, Select, Button, Space, Tag, Tooltip } from 'antd'
import { useTranslation } from 'react-i18next'
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
  value?: FieldFilter[]
  onChange?: (filters: FieldFilter[]) => void
}

export default function DashboardFieldFilters({ availableFields, value, onChange }: DashboardFieldFiltersProps) {
  const { t } = useTranslation()
  const [internalFilters, setInternalFilters] = useState<FieldFilter[]>([])
  const [adding, setAdding] = useState(false)

  const controlled = onChange !== undefined
  const filters = controlled ? (value || []) : internalFilters
  const setFilters = controlled
    ? (f: FieldFilter[]) => { onChange!(f) }
    : setInternalFilters

  const addFilter = useCallback(() => {
    if (availableFields.length === 0) return
    setFilters([...filters, { field: availableFields[0], operator: 'eq', values: [''] }])
    setAdding(false)
  }, [availableFields, filters, setFilters])

  const removeFilter = useCallback((idx: number) => {
    setFilters(filters.filter((_, i) => i !== idx))
  }, [filters, setFilters])

  const updateFilter = useCallback((idx: number, key: keyof FieldFilter, val: string | string[]) => {
    const next = [...filters]
    next[idx] = { ...next[idx], [key]: val }
    setFilters(next)
  }, [filters, setFilters])

  if (availableFields.length === 0) {
    return (
      <div>
        <Space align="start">
          <strong>{t('dashboards.fieldFilters')}</strong>
          <Tooltip title={t('dashboards.noFieldsTooltip')}>
            <InfoCircleOutlined />
          </Tooltip>
        </Space>
        <Input disabled placeholder={t('dashboards.noFieldsPlaceholder')} style={{ marginTop: 8 }} />
      </div>
    )
  }

  return (
    <div>
      <Space align="start" style={{ marginBottom: 8 }}>
        <strong>{t('dashboards.fieldFilters')}</strong>
        <Tooltip title={t('dashboards.fieldFiltersTooltip')}>
          <InfoCircleOutlined />
        </Tooltip>
        {adding ? (
          <>
            <Select
              style={{ width: 160 }}
              options={availableFields.map(f => ({ label: f, value: f }))}
              placeholder={t('dashboards.pickField')}
              onChange={(f) => {
                setFilters([...filters, { field: f, operator: 'eq', values: [''] }])
                setAdding(false)
              }}
              autoFocus
            />
            <Button size="small" onClick={() => setAdding(false)}>{t('common.cancel')}</Button>
          </>
        ) : (
          <Button size="small" icon={<PlusOutlined />} onClick={() => setAdding(true)}>{t('dashboards.addFilter')}</Button>
        )}
      </Space>
      {filters.map((ff, idx) => (
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
              placeholder={t('parsers.value')}
              value={ff.values?.[0] || ''}
              onChange={(e) => updateFilter(idx, 'values', [e.target.value])}
            />
          )}
          <Button type="link" danger size="small" icon={<CloseOutlined />} onClick={() => removeFilter(idx)} />
        </Space>
      ))}
    </div>
  )
}