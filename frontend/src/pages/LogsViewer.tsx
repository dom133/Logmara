import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Table, Input, InputRef, Select, DatePicker, Button, Space, Tag, Card, Typography, Popconfirm, message, Skeleton, Modal, Descriptions } from 'antd'
import { RestOutlined, UnorderedListOutlined, ClockCircleOutlined } from '@ant-design/icons'
import { getLogs, getLogsCount, getDevices, exportCSV, exportHTML, LogEntry, DeviceStats, resolveDeviceDisplayName, sortSupportsCursor } from '../services/api'
import dayjs, { type Dayjs } from 'dayjs'
import { useColumnWidths } from '../hooks/useColumnWidths'
import SeverityTag from '../components/SeverityTag'
import EmptyState from '../components/EmptyState'
import LogTable, { useDeviceMap, buildDefaultColumns, resolveHostname } from '../components/LogTable'
import { DATE_PRESETS } from '../constants'
import { useAuth } from '../services/auth'

const { Title, Text } = Typography
const { RangePicker } = DatePicker

const severities = ['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug']
const INTERVAL_OPTIONS = [1, 3, 5, 10, 30]

export default function LogsViewer() {
  const [searchParams] = useSearchParams()
  const { user } = useAuth()
  const urlHostname = searchParams.get('hostname') || ''
  const urlFromHostIP = searchParams.get('fromhost_ip') || ''
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [totalLogs, setTotalLogs] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
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
  const [pageSize, setPageSize] = useState(50)
  const cursorRef = useRef('')
  const offsetRef = useRef(0)
  const searchRef = useRef<InputRef>(null)
  const [detailModalOpen, setDetailModalOpen] = useState(false)
  const [selectedLog, setSelectedLog] = useState<LogEntry | null>(null)

  const [refreshInterval, setRefreshInterval] = useState(() => {
    const saved = localStorage.getItem('logs_refresh_interval')
    return saved ? parseInt(saved, 10) : 5
  })

  const [appendMode, setAppendMode] = useState(true)

  useEffect(() => {
    localStorage.setItem('logs_refresh_interval', String(refreshInterval))
  }, [refreshInterval])

  const deviceMap = useDeviceMap(devices)

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

  const [isTabActive, setIsTabActive] = useState(true)

  useEffect(() => {
    loadDevices()
    const interval = setInterval(() => {
      if (isTabActive && user) {
        loadDevices()
      }
    }, 30000)

    const handleVisibilityChange = () => {
      setIsTabActive(!document.hidden)
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)

    return () => {
      clearInterval(interval)
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [isTabActive, user])

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

  const loadDevices = async () => {
    const d = await getDevices()
    setDevices(d)
  }

  const loadLogs = useCallback(async (reset: boolean) => {
    const useCursor = sortSupportsCursor(filters.sort)
    const from = filters.from ? dayjs(filters.from).format() : ''
    const to = filters.to ? dayjs(filters.to).format() : ''
    reset ? setLoading(true) : setLoadingMore(true)
    try {
      const data = await getLogs({
        ...filters,
        limit: pageSize,
        cursor: useCursor && !reset ? cursorRef.current : undefined,
        offset: !useCursor && !reset ? offsetRef.current : 0,
        from,
        to,
      })
      setLogs(prev => (reset ? (data.logs || []) : [...prev, ...(data.logs || [])]))
      setHasMore(data.has_more || false)
      cursorRef.current = data.next_cursor || ''
      offsetRef.current = reset ? pageSize : offsetRef.current + pageSize
      if (reset) {
        getLogsCount({
          hostname: filters.hostname,
          fromhost_ip: filters.fromhost_ip,
          severity: filters.severity,
          search: filters.search,
          from,
          to,
        }).then(setTotalLogs).catch(() => {})
      }
    } finally {
      setLoading(false)
      setLoadingMore(false)
    }
  }, [filters, pageSize])

  useEffect(() => {
    loadLogs(true)
  }, [filters, pageSize])

  const pollLogs = useCallback(async () => {
    try {
      const data = await getLogs({
        ...filters,
        limit: pageSize,
        from: filters.from ? dayjs(filters.from).format() : '',
        to: filters.to ? dayjs(filters.to).format() : '',
      })
      setLogs(data.logs || [])
      setHasMore(data.has_more || false)
      cursorRef.current = data.next_cursor || ''
      offsetRef.current = pageSize
    } catch {
      // silent fail on poll
    }
  }, [filters, pageSize])

  useEffect(() => {
    if (!isTabActive || !appendMode || !user) return
    const interval = setInterval(() => {
      pollLogs()
    }, refreshInterval * 1000)
    return () => clearInterval(interval)
  }, [isTabActive, appendMode, pollLogs, refreshInterval, user])

  const handleLoadMore = () => loadLogs(false)

  const handleSearch = (value: string) => {
    setFilters(f => ({ ...f, search: value }))
  }

  const handleDateRange = (dates: [Dayjs | null, Dayjs | null] | null) => {
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

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 8, marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0, whiteSpace: 'nowrap' }}>Logs</Title>
        <Text type="secondary">({totalLogs.toLocaleString()} total)</Text>
        {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>Reset Columns</Button>}
        <Select
          size="small"
          style={{ width: 100 }}
          value={refreshInterval}
          onChange={setRefreshInterval}
          options={INTERVAL_OPTIONS.map(v => ({ label: `${v}s`, value: v }))}
          suffixIcon={<ClockCircleOutlined />}
        />
        <Button
          size="small"
          icon={<UnorderedListOutlined />}
          onClick={() => setAppendMode(!appendMode)}
          style={{ color: appendMode ? '#1890ff' : undefined }}
        >
          Live
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
            options={devices.map(d => ({ label: resolveDeviceDisplayName(d), value: d.fromhost_ip || '__unknown__' }))}
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
          }}
        />
      ) : (
        <>
          {loading && logs.length === 0 && (
            <Skeleton active paragraph={{ rows: 10 }} />
          )}
          <LogTable
            logs={logs}
            devices={devices}
            loading={loading}
            loadingMore={loadingMore}
            hasMore={hasMore}
            pageSize={pageSize}
            setPageSize={setPageSize}
            onLoadMore={handleLoadMore}
            onRowClick={handleRowClick}
            enhanceColumns={enhanceColumns}
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
                <Descriptions.Item label="Device"><Tag color="blue">{resolveHostname(deviceMap, selectedLog.hostname, selectedLog.fromhost_ip, selectedLog.display_name)}</Tag></Descriptions.Item>
                {selectedLog.fromhost_ip && <Descriptions.Item label="Source IP"><Tag color="green">{selectedLog.fromhost_ip}</Tag></Descriptions.Item>}
                {selectedLog.app_name && <Descriptions.Item label="App">{selectedLog.app_name}</Descriptions.Item>}
                <Descriptions.Item label="Message">
                  <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', width: '100%', maxWidth: '100%', fontFamily: 'Consolas, Monaco, monospace', fontSize: 12, lineHeight: 1.4 }}>
                    {selectedLog.raw_message || selectedLog.message}
                  </pre>
                </Descriptions.Item>
                {selectedLog.raw_message && selectedLog.raw_message !== selectedLog.message && (
                  <Descriptions.Item label="Raw Message">
                    <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', width: '100%', maxWidth: '100%', fontFamily: 'Consolas, Monaco, monospace', fontSize: 12, lineHeight: 1.4 }}>
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
