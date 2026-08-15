import { useState, useEffect, useCallback, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
import { Table, Input, InputRef, Select, DatePicker, Button, Space, Tag, Card, Typography, message, Skeleton, Modal, Descriptions } from 'antd'
import { RestOutlined, UnorderedListOutlined, ClockCircleOutlined } from '@ant-design/icons'
import { getLogs, getLogsCount, getDevices, exportCSV, exportHTML, LogEntry, DeviceStats, resolveDeviceDisplayName, sortSupportsCursor } from '../services/api'
import dayjs, { type Dayjs } from 'dayjs'
import { useColumnWidths } from '../hooks/useColumnWidths'
import SeverityTag from '../components/SeverityTag'
import EmptyState from '../components/EmptyState'
import LogTable, { useDeviceMap, buildDefaultColumns, resolveHostname } from '../components/LogTable'
import ExportDialog from '../components/ExportDialog'
import { getDatePresets } from '../constants'
import { useAuth } from '../services/auth'

const { Title, Text } = Typography
const { RangePicker } = DatePicker

const severities = ['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug']
const INTERVAL_OPTIONS = [1, 3, 5, 10, 30]

export default function LogsViewer() {
  const { t } = useTranslation()
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
  const [exportOpen, setExportOpen] = useState(false)
  const [exportFormat, setExportFormat] = useState<'csv' | 'html'>('csv')

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
    setExportFormat(format)
    setExportOpen(true)
  }

  const handleExportConfirm = async (params: Record<string, string>) => {
    if (exportFormat === 'csv') await exportCSV(params)
    else await exportHTML(params)
    setExportOpen(false)
    message.success(t('dashboard.exporting', { format: exportFormat.toUpperCase() }))
  }

  const handleRowClick = (record: LogEntry) => {
    setSelectedLog(record)
    setDetailModalOpen(true)
  }

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 8, marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0, whiteSpace: 'nowrap' }}>{t('nav.logs')}</Title>
        <Text type="secondary">({totalLogs.toLocaleString()} {t('logs.total')})</Text>
        {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>{t('dashboards.resetColumns')}</Button>}
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
          {t('dashboard.live')}
        </Button>
      </div>

      <Card style={{ marginBottom: 16 }} size="small">
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <Input
            ref={searchRef}
            placeholder={t('logs.searchPlaceholder')}
            style={{ minWidth: 200, flex: '1 1 200px' }}
            allowClear
            onChange={e => handleSearch(e.target.value)}
            value={filters.search}
          />
          <Select
            placeholder={t('dashboard.device')}
            style={{ minWidth: 140, flex: '1 1 140px' }}
            allowClear
            options={devices.map(d => ({ label: resolveDeviceDisplayName(d), value: d.fromhost_ip || '__unknown__' }))}
            value={filters.fromhost_ip || undefined}
            onChange={v => setFilters(f => ({ ...f, fromhost_ip: v || '' }))}
          />
          <Select
            placeholder={t('dashboard.severity')}
            style={{ minWidth: 100, flex: '1 1 100px' }}
            allowClear
            options={severities.map(s => ({ label: s.toUpperCase(), value: s }))}
            value={filters.severity || undefined}
            onChange={v => setFilters(f => ({ ...f, severity: v || '' }))}
          />
          <Select
            placeholder={t('logs.datePreset')}
            style={{ minWidth: 120, flex: '1 1 120px' }}
            allowClear
            options={getDatePresets(t)}
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
            placeholder={t('dashboard.sort')}
            style={{ minWidth: 130, flex: '1 1 130px' }}
            value={filters.sort}
            onChange={v => setFilters(f => ({ ...f, sort: v }))}
            options={[
              { label: t('dashboard.newestFirst'), value: 'timestamp_desc' },
              { label: t('dashboard.oldestFirst'), value: 'timestamp_asc' },
              { label: t('dashboard.bySeverity'), value: 'severity' },
              { label: t('dashboard.byDevice'), value: 'hostname' },
            ]}
          />
          <Button onClick={() => handleExport('csv')}>{t('dashboard.csv')}</Button>
          <Button onClick={() => handleExport('html')}>{t('dashboard.html')}</Button>
        </div>
      </Card>

      {!loading && logs.length === 0 ? (
        <EmptyState
          description={t('logs.noLogsFound')}
          actionLabel={t('logs.clearFilters')}
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
            title={t('dashboard.logDetails')}
            open={detailModalOpen}
            onCancel={() => setDetailModalOpen(false)}
            footer={[
              <Button key="close" onClick={() => setDetailModalOpen(false)} style={{ width: '100%' }}>{t('common.close')}</Button>
            ]}
            width={{ sm: '90%', md: 700 }}
          >
            {selectedLog && (
              <Descriptions bordered column={1} size="small">
                <Descriptions.Item label={t('dashboard.timestamp')}>{new Date(selectedLog.timestamp).toLocaleString()}</Descriptions.Item>
                <Descriptions.Item label={t('dashboard.severity')}><SeverityTag severity={selectedLog.severity} /></Descriptions.Item>
                <Descriptions.Item label={t('dashboard.device')}><Tag color="blue">{resolveHostname(deviceMap, selectedLog.hostname, selectedLog.fromhost_ip, selectedLog.display_name)}</Tag></Descriptions.Item>
                {selectedLog.fromhost_ip && <Descriptions.Item label={t('dashboard.sourceIp')}><Tag color="green">{selectedLog.fromhost_ip}</Tag></Descriptions.Item>}
                {selectedLog.app_name && <Descriptions.Item label={t('dashboard.app')}>{selectedLog.app_name}</Descriptions.Item>}
                <Descriptions.Item label={t('dashboard.message')}>
                  <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', width: '100%', maxWidth: '100%', fontFamily: 'Consolas, Monaco, monospace', fontSize: 12, lineHeight: 1.4 }}>
                    {selectedLog.raw_message || selectedLog.message}
                  </pre>
                </Descriptions.Item>
                {selectedLog.raw_message && selectedLog.raw_message !== selectedLog.message && (
                  <Descriptions.Item label={t('logs.rawMessage')}>
                    <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', width: '100%', maxWidth: '100%', fontFamily: 'Consolas, Monaco, monospace', fontSize: 12, lineHeight: 1.4 }}>
                      {selectedLog.raw_message}
                    </pre>
                  </Descriptions.Item>
                )}
                <Descriptions.Item label={t('dashboard.matchedParsers')}>
                  {selectedLog.matched_parsers && selectedLog.matched_parsers.length > 0
                    ? selectedLog.matched_parsers.map(p => <Tag key={p} color="purple">{p}</Tag>)
                    : t('common.none')}
                </Descriptions.Item>
              </Descriptions>
            )}
          </Modal>
          <ExportDialog
            open={exportOpen}
            onCancel={() => setExportOpen(false)}
            format={exportFormat}
            onExport={handleExportConfirm}
            filters={filters}
            deviceLabel={filters.fromhost_ip ? resolveDeviceDisplayName(devices.find(d => d.fromhost_ip === filters.fromhost_ip) || { fromhost_ip: filters.fromhost_ip } as DeviceStats) : undefined}
          />
        </>
      )}
    </>
  )
}
