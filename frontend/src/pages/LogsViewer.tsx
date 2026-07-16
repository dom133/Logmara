import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Table, Input, InputRef, Select, DatePicker, Button, Space, Tag, Card, Typography, Popconfirm, message, Skeleton, Dropdown, Modal, Descriptions } from 'antd'
import { RestOutlined, ColumnHeightOutlined, ClusterOutlined, UnorderedListOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { getLogs, getDevices, exportCSV, exportHTML, LogEntry, DeviceStats, resolveDeviceDisplayName } from '../services/api'
import dayjs from 'dayjs'
import { useColumnWidths } from '../hooks/useColumnWidths'
import { useSSE } from '../hooks/useSSE'
import SeverityTag from '../components/SeverityTag'
import EmptyState from '../components/EmptyState'
import { DATE_PRESETS } from '../constants'

const { Title, Text } = Typography
const { RangePicker } = DatePicker

const severities = ['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug']

export default function LogsViewer() {
  const [searchParams] = useSearchParams()
  const urlHostname = searchParams.get('hostname') || ''
  const urlFromHostIP = searchParams.get('fromhost_ip') || ''
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [devices, setDevices] = useState<DeviceStats[]>([])
  const [filters, setFilters] = useState({
    hostname: urlHostname,
    fromhost_ip: urlFromHostIP,
    severity: '',
    search: '',
    from: '',
    to: '',
    sort: 'timestamp_desc',
  })
  const [pagination, setPagination] = useState({ current: 1, pageSize: 50 })
  const [visibleColumns, setVisibleColumns] = useState<string[]>(['timestamp', 'severity', 'hostname', 'app_name', 'message'])
  const searchRef = useRef<InputRef>(null)
  const [detailModalOpen, setDetailModalOpen] = useState(false)
  const [selectedLog, setSelectedLog] = useState<LogEntry | null>(null)
  const [groupByDevice, setGroupByDevice] = useState(false)
  const [streaming, setStreaming] = useState(false)
  const logsRef = useRef(logs)
  logsRef.current = logs

  const deviceMap = useMemo(() => {
    const m = new Map<string, string>()
    for (const d of devices) {
      const dn = resolveDeviceDisplayName(d)
      if (d.fromhost_ip) m.set(d.fromhost_ip, dn)
      if (d.hostname) m.set(d.hostname, dn)
      if (d.old_hostname) m.set(d.old_hostname, dn)
    }
    return m
  }, [devices])

  const resolveHostname = (hostname: string, fromhost_ip?: string): string => {
    return deviceMap.get(fromhost_ip || hostname || '') || hostname || '-'
  }

  const { enhanceColumns, hasChanges, reset } = useColumnWidths(
    'col_widths_logs',
    [
      { key: 'timestamp', width: 180 },
      { key: 'severity', width: 90 },
      { key: 'hostname', width: 160 },
      { key: 'app_name', width: 120 },
      { key: 'message', width: 300 },
    ],
  )

  useEffect(() => {
    loadDevices()
    const interval = setInterval(loadDevices, 30000)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
        e.preventDefault()
        searchRef.current?.focus()
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [])

  const handleNewLogs = useCallback((newLogs: LogEntry[]) => {
    setLogs(prev => {
      const ids = new Set(prev.map(l => l.id))
      const unique = newLogs.filter(l => !ids.has(l.id))
      if (unique.length === 0) return prev
      setTotal(t => t + unique.length)
      const combined = [...unique, ...prev]
      return combined.slice(0, pagination.pageSize * 3)
    })
  }, [pagination.pageSize])

  const { connected } = useSSE({
    onNewLogs: handleNewLogs,
    filters: {
      hostname: filters.hostname || undefined,
      fromhost_ip: filters.fromhost_ip || undefined,
      severity: filters.severity || undefined,
      search: filters.search || undefined,
      from: filters.from || undefined,
      to: filters.to || undefined,
    },
    enabled: streaming,
  })

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
    if (filters.fromhost_ip) params.fromhost_ip = filters.fromhost_ip
    if (filters.severity) params.severity = filters.severity
    if (filters.search) params.search = filters.search
    if (filters.from) params.from = filters.from
    if (filters.to) params.to = filters.to

    if (format === 'csv') exportCSV(params)
    else exportHTML(params)
    message.success(`Exporting ${format.toUpperCase()}...`)
  }

  const handleRowClick = (record: LogEntry) => {
    setSelectedLog(record)
    setDetailModalOpen(true)
  }

  const getRowSpans = (records: LogEntry[]) => {
    if (!groupByDevice) return new Map()
    const spans = new Map<number, number>()
    let i = 0
    while (i < records.length) {
      let j = i + 1
      while (j < records.length && (records[j].fromhost_ip || '') === (records[i].fromhost_ip || '')) j++
      const span = j - i
      for (let k = i; k < j; k++) spans.set(k, k === i ? span : 0)
      i = j
    }
    return spans
  }

  const columns = [
    {
      title: 'Time',
      dataIndex: 'timestamp',
      key: 'timestamp',
      width: 180,
      render: (v: string) => new Date(v).toLocaleString(),
      sorter: (a: LogEntry, b: LogEntry) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
      defaultSortOrder: 'descend',
    },
    {
      title: 'Severity',
      dataIndex: 'severity',
      key: 'severity',
      width: 90,
      render: (v: string) => <SeverityTag severity={v} />,
      filters: severities.map(s => ({ text: s.toUpperCase(), value: s })),
      onFilter: (v: any, record: LogEntry) => record.severity === String(v),
    },
    {
      title: 'Device',
      dataIndex: 'hostname',
      key: 'hostname',
      width: 160,
      render: (v: string | null | undefined, _record: LogEntry, index: number) => {
        const rowSpans = getRowSpans(logs)
        return {
          props: { rowSpan: rowSpans.get(index) },
          children: v ? <Tag color="blue">{resolveHostname(v, _record.fromhost_ip)}</Tag> : '-',
        }
      },
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
      <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 8, marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0, whiteSpace: 'nowrap' }}>Logs</Title>
        <Text type="secondary">({total.toLocaleString()} total)</Text>
        {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>Reset Columns</Button>}
        <Dropdown
          menu={{
            items: [
              { key: 'timestamp', label: 'Time' },
              { key: 'severity', label: 'Severity' },
              { key: 'hostname', label: 'Device' },
              { key: 'app_name', label: 'App' },
              { key: 'message', label: 'Message' },
            ],
            selectable: true,
            selectedKeys: visibleColumns,
            onSelect: (keyOrInfo) => {
              const key = typeof keyOrInfo === 'string' ? keyOrInfo : keyOrInfo.key
              setVisibleColumns(prev =>
                prev.includes(key) ? prev.filter(c => c !== key) : [...prev, key],
              )
            },
          }}
          placement="bottomRight"
        >
          <Button size="small" icon={<ColumnHeightOutlined />}>Columns</Button>
        </Dropdown>
        <Button
          size="small"
          icon={groupByDevice ? <ClusterOutlined /> : <UnorderedListOutlined />}
          onClick={() => setGroupByDevice(!groupByDevice)}
          style={{ color: groupByDevice ? '#1890ff' : undefined }}
        >
          Group by Device
        </Button>
        <Button
          size="small"
          icon={<ThunderboltOutlined />}
          type={streaming ? 'primary' : 'default'}
          onClick={() => setStreaming(!streaming)}
          style={{ color: streaming && connected ? '#52c41a' : undefined }}
        >
          {streaming ? (connected ? 'Live ●' : 'Connecting...') : 'Live'}
        </Button>
      </div>

      <Card style={{ marginBottom: 16 }} size="small">
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <Input
            ref={searchRef}
            placeholder="Search messages... (Ctrl+K)"
            style={{ minWidth: 200, flex: '1 1 200px' }}
            allowClear
            onChange={e => handleSearch(e.target.value)}
            value={filters.search}
          />
          <Select
            placeholder="Device"
            style={{ minWidth: 140, flex: '1 1 140px' }}
            allowClear
            options={[{ label: 'Unknown', value: '__unknown__' }, ...devices.map(d => ({ label: resolveDeviceDisplayName(d), value: d.fromhost_ip }))]}
            value={filters.fromhost_ip || undefined}
            onChange={v => setFilters(f => ({ ...f, fromhost_ip: v || '' }))}
          />
          <Select
            placeholder="Severity"
            style={{ minWidth: 100, flex: '1 1 100px' }}
            allowClear
            options={severities.map(s => ({ label: s.toUpperCase(), value: s }))}
            value={filters.severity || undefined}
            onChange={v => setFilters(f => ({ ...f, severity: v || '' }))}
          />
          <Select
            placeholder="Date Preset"
            style={{ minWidth: 120, flex: '1 1 120px' }}
            allowClear
            options={DATE_PRESETS}
            onChange={v => {
              const now = dayjs()
              const to = now.format()
              const from = now.subtract(parseInt(v), v.endsWith('h') ? 'hour' : 'day').format()
              setFilters(f => ({ ...f, from, to }))
            }}
          />
          <RangePicker
            style={{ minWidth: 240, flex: '1 1 240px' }}
            onChange={handleDateRange}
          />
          <Select
            placeholder="Sort"
            style={{ minWidth: 130, flex: '1 1 130px' }}
            value={filters.sort}
            onChange={v => setFilters(f => ({ ...f, sort: v }))}
            options={[
              { label: 'Newest First', value: 'timestamp_desc' },
              { label: 'Oldest First', value: 'timestamp_asc' },
              { label: 'By Severity', value: 'severity' },
              { label: 'By Device', value: 'hostname' },
            ]}
          />
          <Popconfirm title="Export as CSV?" onConfirm={() => handleExport('csv')}>
            <Button>CSV</Button>
          </Popconfirm>
          <Popconfirm title="Export as HTML?" onConfirm={() => handleExport('html')}>
            <Button>HTML</Button>
          </Popconfirm>
        </div>
      </Card>

      {!loading && logs.length === 0 ? (
        <EmptyState
          description="No logs found. Try adjusting your filters."
          actionLabel="Clear Filters"
          actionClick={() => {
            setFilters({ hostname: '', fromhost_ip: '', severity: '', search: '', from: '', to: '', sort: 'timestamp_desc' })
            setPagination({ current: 1, pageSize: 50 })
          }}
        />
      ) : (
        <>
          {loading && logs.length === 0 && (
            <Skeleton active paragraph={{ rows: 10 }} />
          )}
          <Table
            columns={enhanceColumns(columns.filter(c => visibleColumns.includes(c.key)))}
            dataSource={logs}
            rowKey="id"
            loading={loading}
            scroll={{ x: 'max-content' }}
            pagination={{
              ...pagination,
              total,
              showSizeChanger: true,
              showQuickJumper: true,
              pageSizeOptions: ['25', '50', '100', '200'],
            }}
            onChange={handleTableChange}
            size="small"
            onRow={(record) => ({
              onClick: () => handleRowClick(record),
              style: { cursor: 'pointer' },
            })}
          />
          <Modal
            title="Log Details"
            open={detailModalOpen}
            onCancel={() => setDetailModalOpen(false)}
            footer={[
              <Button key="close" onClick={() => setDetailModalOpen(false)} style={{ width: '100%' }}>Close</Button>
            ]}
            width={{ sm: '90%', md: 700 }}
          >
            {selectedLog && (
              <Descriptions bordered column={1} size="small">
                <Descriptions.Item label="Timestamp">{new Date(selectedLog.timestamp).toLocaleString()}</Descriptions.Item>
                <Descriptions.Item label="Severity"><SeverityTag severity={selectedLog.severity} /></Descriptions.Item>
                <Descriptions.Item label="Device"><Tag color="blue">{resolveHostname(selectedLog.hostname, selectedLog.fromhost_ip)}</Tag></Descriptions.Item>
                {selectedLog.fromhost_ip && <Descriptions.Item label="Source IP"><Tag color="green">{selectedLog.fromhost_ip}</Tag></Descriptions.Item>}
                {selectedLog.app_name && <Descriptions.Item label="App">{selectedLog.app_name}</Descriptions.Item>}
                <Descriptions.Item label="Message">
                  <pre style={{ whiteSpace: 'pre-wrap', fontFamily: 'Consolas, Monaco, monospace', fontSize: 12, lineHeight: 1.4 }}>
                    {selectedLog.raw_message || selectedLog.message}
                  </pre>
                </Descriptions.Item>
                {selectedLog.raw_message && selectedLog.raw_message !== selectedLog.message && (
                  <Descriptions.Item label="Raw Message">
                    <pre style={{ whiteSpace: 'pre-wrap', fontFamily: 'Consolas, Monaco, monospace', fontSize: 12, lineHeight: 1.4 }}>
                      {selectedLog.raw_message}
                    </pre>
                  </Descriptions.Item>
                )}
                <Descriptions.Item label="Matched Parsers">
                  {selectedLog.matched_parsers && selectedLog.matched_parsers.length > 0
                    ? selectedLog.matched_parsers.map(p => <Tag key={p} color="purple">{p}</Tag>)
                    : 'None'}
                </Descriptions.Item>
              </Descriptions>
            )}
          </Modal>
        </>
      )}
    </>
  )
}