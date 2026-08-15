import { useState, useEffect, useCallback } from 'react'
import { Table, Input, Select, DatePicker, Button, Space, Tag, Card, Typography, Popconfirm, message } from 'antd'
import { getLogs, getDevices, exportCSV, exportHTML } from '../services/api'
import { LogEntry } from '../services/api'
import dayjs from 'dayjs'

const { Title, Text } = Typography
const { RangePicker } = DatePicker

const severityColors: Record<string, string> = {
  emerg: '#f5222d', alert: '#ff4d4f', crit: '#ff7a45', err: '#faad14',
  warning: '#fadb14', notice: '#1890ff', info: '#52c41a', debug: '#bfbfbf',
}

const severities = ['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug']

export default function LogsViewer() {
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [devices, setDevices] = useState<string[]>([])
  const [filters, setFilters] = useState({
    hostname: '',
    severity: '',
    search: '',
    from: '',
    to: '',
    sort: 'timestamp_desc',
  })
  const [pagination, setPagination] = useState({ current: 1, pageSize: 50 })

  useEffect(() => {
    loadDevices()
  }, [])

  useEffect(() => {
    loadLogs(0)
  }, [filters])

  const loadDevices = async () => {
    const d = await getDevices()
    setDevices(d)
  }

  const loadLogs = useCallback(async (offset: number) => {
    setLoading(true)
    try {
      const data = await getLogs({
        ...filters,
        offset,
        limit: pagination.pageSize,
        from: filters.from ? dayjs(filters.from).format() : '',
        to: filters.to ? dayjs(filters.to).format() : '',
      })
      setLogs(data.logs || [])
      setTotal(data.total || 0)
    } finally {
      setLoading(false)
    }
  }, [filters, pagination.pageSize])

  const handleTableChange = (pag: any) => {
    setPagination(pag)
    loadLogs((pag.current - 1) * pag.pageSize)
  }

  const handleSearch = (value: string) => {
    setFilters(f => ({ ...f, search: value }))
  }

  const handleDateRange = (dates: any) => {
    setFilters(f => ({
      ...f,
      from: dates?.[0]?.toISOString() || '',
      to: dates?.[1]?.toISOString() || '',
    }))
  }

  const handleExport = (format: 'csv' | 'html') => {
    const params: Record<string, string> = {}
    if (filters.hostname) params.hostname = filters.hostname
    if (filters.severity) params.severity = filters.severity
    if (filters.search) params.search = filters.search
    if (filters.from) params.from = filters.from
    if (filters.to) params.to = filters.to

    if (format === 'csv') exportCSV(params)
    else exportHTML(params)
    message.success(`Exporting ${format.toUpperCase()}...`)
  }

  const columns = [
    {
      title: 'Time',
      dataIndex: 'timestamp',
      key: 'timestamp',
      width: 180,
      fixed: 'left' as const,
      render: (v: string) => <Text style={{ fontFamily: 'monospace', fontSize: 12 }}>{new Date(v).toLocaleString()}</Text>,
      sorter: true,
    },
    {
      title: 'Severity',
      dataIndex: 'severity',
      key: 'severity',
      width: 90,
      render: (v: string) => <Tag color={severityColors[v] || 'default'}>{v.toUpperCase()}</Tag>,
      filters: severities.map(s => ({ text: s.toUpperCase(), value: s })),
      onFilter: (v: any, record: LogEntry) => record.severity === String(v),
    },
    {
      title: 'Source IP',
      dataIndex: 'hostname',
      key: 'hostname',
      width: 160,
      render: (v: string) => <Tag color="blue">{v}</Tag>,
    },
    {
      title: 'App',
      dataIndex: 'app_name',
      key: 'app_name',
      width: 120,
      render: (v?: string) => v ? <Text type="secondary">{v}</Text> : '-',
    },
    {
      title: 'Message',
      dataIndex: 'message',
      key: 'message',
      ellipsis: { showTitle: true },
      render: (v: string, record: LogEntry) => {
        const display = record.raw_message || v
        return (
          <pre style={{
            margin: 0,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            fontFamily: 'Consolas, Monaco, monospace',
            fontSize: 12,
            lineHeight: 1.4,
            maxHeight: 100,
            overflow: 'auto',
          }}>
            {display}
          </pre>
        )
      },
    },
  ]

  return (
    <>
      <Space style={{ marginBottom: 16 }} align="center">
        <Title level={3} style={{ margin: 0 }}>Logs</Title>
        <Text type="secondary">({total.toLocaleString()} total)</Text>
      </Space>

      <Card style={{ marginBottom: 16 }} size="small">
        <Space wrap>
          <Input
            placeholder="Search messages..."
            style={{ width: 280 }}
            allowClear
            onChange={e => handleSearch(e.target.value)}
            value={filters.search}
          />
          <Select
            placeholder="Source IP"
            style={{ width: 180 }}
            allowClear
            options={devices.map(d => ({ label: d, value: d }))}
            value={filters.hostname || undefined}
            onChange={v => setFilters(f => ({ ...f, hostname: v || '' }))}
          />
          <Select
            placeholder="Severity"
            style={{ width: 130 }}
            allowClear
            options={severities.map(s => ({ label: s.toUpperCase(), value: s }))}
            value={filters.severity || undefined}
            onChange={v => setFilters(f => ({ ...f, severity: v || '' }))}
          />
          <RangePicker
            style={{ width: 300 }}
            onChange={handleDateRange}
          />
          <Select
            placeholder="Sort"
            style={{ width: 150 }}
            value={filters.sort}
            onChange={v => setFilters(f => ({ ...f, sort: v }))}
            options={[
              { label: 'Newest First', value: 'timestamp_desc' },
              { label: 'Oldest First', value: 'timestamp_asc' },
              { label: 'By Severity', value: 'severity' },
              { label: 'By Source IP', value: 'hostname' },
            ]}
          />
          <Popconfirm title="Export as CSV?" onConfirm={() => handleExport('csv')}>
            <Button icon="📄">CSV</Button>
          </Popconfirm>
          <Popconfirm title="Export as HTML?" onConfirm={() => handleExport('html')}>
            <Button icon="📑">HTML</Button>
          </Popconfirm>
        </Space>
      </Card>

      <Table
        columns={columns}
        dataSource={logs}
        rowKey="id"
        loading={loading}
        scroll={{ x: 1200 }}
        pagination={{
          ...pagination,
          total,
          showSizeChanger: true,
          showQuickJumper: true,
          pageSizeOptions: ['25', '50', '100', '200'],
        }}
        onChange={handleTableChange}
        size="small"
      />
    </>
  )
}