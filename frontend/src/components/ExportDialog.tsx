import { useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Modal, Form, Select, DatePicker, Button, Space, Tag, Alert, message } from 'antd'
import { ExportOutlined, LoadingOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'

const { RangePicker } = DatePicker

const LIMIT_OPTIONS = [
  { label: '1,000', value: '1000' },
  { label: '10,000', value: '10000' },
  { label: '100,000', value: '100000' },
  { label: 'export.noLimit', value: '' },
]

interface ExportDialogProps {
  open: boolean
  onCancel: () => void
  format: 'csv' | 'html'
  onExport: (params: Record<string, string>) => void
  filters: {
    fromhost_ip?: string
    severity?: string
    search?: string
    from?: string
    to?: string
  }
  deviceLabel?: string
}

export default function ExportDialog({ open, onCancel, format, onExport, filters, deviceLabel }: ExportDialogProps) {
  const { t } = useTranslation()
  const [form] = Form.useForm()
  const [loading, setLoading] = useState(false)

  const handleOk = useCallback(async () => {
    try {
      const values = await form.validateFields()
      setLoading(true)
      const params: Record<string, string> = {}
      if (values.fromhost_ip) params.fromhost_ip = values.fromhost_ip
      if (values.severity) params.severity = values.severity
      if (values.search) params.search = values.search
      if (values.dateRange?.[0]) params.from = values.dateRange[0].toISOString()
      if (values.dateRange?.[1]) params.to = values.dateRange[1].toISOString()
      params.limit = values.limit || ''
      onExport(params)
      setLoading(false)
      form.resetFields()
    } catch {
      // validation failed
    }
  }, [form, onExport])

  const handleCancel = useCallback(() => {
    form.resetFields()
    setLoading(false)
    onCancel()
  }, [form, onCancel])

  const initialDates = [filters.from ? dayjs(filters.from) : null, filters.to ? dayjs(filters.to) : null] as [Dayjs | null, Dayjs | null]

  return (
    <Modal
      title={t(`dashboard.export${format === 'csv' ? 'Csv' : 'Html'}`)}
      open={open}
      onCancel={handleCancel}
      maskClosable={!loading}
      closable={!loading}
      footer={[
        <Button key="cancel" disabled={loading} onClick={handleCancel}>
          {t('common.cancel')}
        </Button>,
        <Button key="export" type="primary" icon={loading ? <LoadingOutlined /> : <ExportOutlined />} loading={loading} onClick={handleOk}>
          {t('logs.export')}
        </Button>,
      ]}
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          fromhost_ip: filters.fromhost_ip || undefined,
          severity: filters.severity || undefined,
          search: filters.search || undefined,
          dateRange: initialDates[0] && initialDates[1] ? initialDates : null,
          limit: '100000',
        }}
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          description={
            <div>
              <div><strong>{t('logs.dateRange')}:</strong> {t('export.dateRangeRequired')}</div>
              <div><strong>{t('logs.exportFormat')}:</strong> {format.toUpperCase()}</div>
            </div>
          }
        />

        <Form.Item name="dateRange" label={t('logs.dateRange')} rules={[{ required: true, message: t('export.dateRangeRequired') }]}>
          <RangePicker showTime={{ format: 'HH:mm:ss' }} style={{ width: '100%' }} />
        </Form.Item>

        <Form.Item name="limit" label={t('export.rowLimit')} tooltip={t('export.rowLimitTooltip')}>
          <Select
            options={LIMIT_OPTIONS.map(opt => ({
              ...opt,
              label: t(opt.label as string),
            }))}
          />
        </Form.Item>

        <Form.Item label={t('export.activeFilters')}>
          <Space wrap>
            {filters.fromhost_ip && deviceLabel ? (
              <Tag color="blue">{deviceLabel}</Tag>
            ) : filters.fromhost_ip ? (
              <Tag color="blue">{filters.fromhost_ip}</Tag>
            ) : null}
            {filters.severity && <Tag color="orange">{filters.severity.toUpperCase()}</Tag>}
            {filters.search && <Tag color="green">{filters.search}</Tag>}
            {!filters.fromhost_ip && !filters.severity && !filters.search && <Tag>{t('common.none')}</Tag>}
          </Space>
        </Form.Item>
      </Form>
    </Modal>
  )
}
