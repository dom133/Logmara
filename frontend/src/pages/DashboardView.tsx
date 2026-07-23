import { useEffect, useState, useCallback, useRef, useMemo } from 'react'
import { Card, Table, Button, Tag, Space, Breadcrumb, Spin, Typography, Input, InputRef, Select, Row, Col, Statistic, Descriptions, Modal, DatePicker, Form, Popconfirm, message } from 'antd'
import { ArrowLeftOutlined, FilterOutlined, PushpinOutlined, PushpinFilled, RestOutlined, GlobalOutlined, ClockCircleOutlined, UnorderedListOutlined } from '@ant-design/icons'
import { useNavigate, useParams } from 'react-router-dom'
import { getDashboard, getDashboardData, getDashboardDataCount, exportDashboardCSV, exportDashboardHTML, togglePinDashboard, togglePublicDashboard, Dashboard, LogEntry, getDevices, DeviceStats, resolveDeviceDisplayName, sortSupportsCursor } from '../services/api'
import dayjs, { type Dayjs } from 'dayjs'
import { useColumnWidths } from '../hooks/useColumnWidths'
import SeverityTag from '../components/SeverityTag'
import { SEVERITY_LABELS } from '../constants'
import { useAuth } from '../services/auth'

const { Title, Text } = Typography
const { RangePicker } = DatePicker

const severities = ['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug']
const INTERVAL_OPTIONS = [1, 3, 5, 10, 30]

export default function DashboardViewPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [dashboard, setDashboard] = useState<Dashboard | null>(null)
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [totalLogs, setTotalLogs] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [tableLoading, setTableLoading] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [pageSize, setPageSize] = useState(50)
  // Keyset pagination cursor for "Load more" on the timestamp sorts; sorting
  // by severity/hostname falls back to offset (see sortSupportsCursor).
  const cursorRef = useRef('')
  const offsetRef = useRef(0)
  const [filters, setFilters] = useState({
    search: '',
    severity: '',
    fromhost_ip: '',
    from: '',
    to: '',
    sort: 'timestamp_desc',
  })
  const [detailLog, setDetailLog] = useState<LogEntry | null>(null)
  const [devices, setDevices] = useState<DeviceStats[]>([])
  const { user } = useAuth()
  const isOwner = dashboard?.owner_id === user?.id
  const searchRef = useRef<InputRef>(null)

  const dashboardId = parseInt(id || '0')

  const { enhanceColumns, hasChanges, reset } = useColumnWidths(
    `col_widths_dashboardview_${dashboardId}`,
    [
      { key: 'timestamp', width: 180 },
      { key: 'hostname', width: 150 },
      { key: 'severity', width: 100 },
      { key: 'message', width: 300 },
    ],
  )

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

  const resolveHostname = (hostname: string, fromhost_ip?: string, displayName?: string): string => {
    if (displayName) return displayName
    return deviceMap.get(fromhost_ip || hostname || '') || hostname || '-'
  }

  const loadDashboard = async () => {
    setLoading(true)
    try {
      const d = await getDashboard(dashboardId)
      setDashboard(d)
    } catch (e) {
      navigate('/')
    } finally {
      setLoading(false)
    }
  }

  const loadDevices = async () => {
    const d = await getDevices()
    setDevices(d)
  }

  const [isTabActive, setIsTabActive] = useState(true)
  const [refreshInterval, setRefreshInterval] = useState(() => {
    const saved = localStorage.getItem(`dashboard_refresh_interval_${dashboardId}`)
    return saved ? parseInt(saved, 10) : 5
  })
  const [appendMode, setAppendMode] = useState(true)

  const loadLogs = useCallback(async (reset: boolean) => {
    reset ? setTableLoading(true) : setLoadingMore(true)
    try {
      const from = filters.from ? dayjs(filters.from).format() : ''
      const to = filters.to ? dayjs(filters.to).format() : ''
      const useCursor = sortSupportsCursor(filters.sort)
      const data = await getDashboardData(
        dashboardId, pageSize,
        useCursor && !reset ? cursorRef.current : '',
        filters.search, filters.severity, from, to,
        filters.sort, !useCursor && !reset ? offsetRef.current : 0,
        filters.fromhost_ip,
      )
      setLogs(prev => (reset ? data.logs : [...prev, ...data.logs]))
      setHasMore(data.has_more)
      cursorRef.current = data.next_cursor
      offsetRef.current = reset ? pageSize : offsetRef.current + pageSize
      if (reset) {
        getDashboardDataCount(dashboardId, filters.search, filters.severity, from, to, filters.fromhost_ip)
          .then(setTotalLogs).catch(() => {})
      }
    } catch (e) {
      // error handled by API
    } finally {
      setTableLoading(false)
      setLoadingMore(false)
    }
  }, [dashboardId, pageSize, filters])

  const handleLoadMore = () => loadLogs(false)

  const pollLogs = useCallback(async () => {
    if (!appendMode) return
    try {
      const from = filters.from ? dayjs(filters.from).format() : ''
      const to = filters.to ? dayjs(filters.to).format() : ''
      const data = await getDashboardData(dashboardId, pageSize, '', filters.search, filters.severity, from, to, filters.sort, 0, filters.fromhost_ip)
      setHasMore(data.has_more)
      cursorRef.current = data.next_cursor
      offsetRef.current = pageSize
      setLogs(data.logs)
    } catch (e) {
      // error handled by API
    }
  }, [dashboardId, pageSize, filters, appendMode])

  const handleExport = (format: 'csv' | 'html') => {
    const from = filters.from ? dayjs(filters.from).format() : ''
    const to = filters.to ? dayjs(filters.to).format() : ''
    const params: Record<string, string> = {}
    if (filters.severity) params.severity = filters.severity
    if (filters.fromhost_ip) params.fromhost_ip = filters.fromhost_ip
    if (filters.search) params.search = filters.search
    if (from) params.from = from
    if (to) params.to = to

    if (format === 'csv') exportDashboardCSV(dashboardId, params)
    else exportDashboardHTML(dashboardId, params)
    message.success(`Exporting ${format.toUpperCase()}...`)
  }

  useEffect(() => {
    loadDashboard()
    loadDevices()
    const interval = setInterval(() => {
      if (isTabActive && user) {
        loadDevices()
      }
    }, 30000)

    // Check if tab is active
    const handleVisibilityChange = () => {
      setIsTabActive(!document.hidden)
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)

    return () => {
      clearInterval(interval)
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [dashboardId, isTabActive, user])

  useEffect(() => {
    if (dashboard) {
      loadLogs(true)
    }
  }, [dashboard, loadLogs])

  useEffect(() => {
    localStorage.setItem(`dashboard_refresh_interval_${dashboardId}`, refreshInterval.toString())
  }, [refreshInterval, dashboardId])

  useEffect(() => {
    if (!isTabActive || !user) return
    const interval = setInterval(() => {
      pollLogs()
    }, refreshInterval * 1000)
    return () => clearInterval(interval)
  }, [isTabActive, pollLogs, refreshInterval, user])

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

  const handleTogglePin = async () => {
    if (!dashboard) return
    try {
      const res = await togglePinDashboard(dashboardId)
      setDashboard({ ...dashboard, pinned: res.pinned })
      window.dispatchEvent(new CustomEvent('dashboards-pinned-changed'))
    } catch (e) {
      // error handled by API
    }
  }

  const handleTogglePublic = async () => {
    if (!dashboard) return
    try {
      const res = await togglePublicDashboard(dashboardId)
      setDashboard({ ...dashboard, is_public: res.is_public })
      message.success(res.is_public ? 'Dashboard is now public' : 'Dashboard is now private')
    } catch (e) {
      // error handled by API
    }
  }

  const fields = dashboard?.config?.fields || []
  const dashDevices = dashboard?.config?.devices || []
  const dashFixedSeverity = dashboard?.config?.filters?.severity || ''

  // Device filter options limited to the dashboard's own device scope -
  // narrowing to a device outside that scope wouldn't return anything
  // anyway (see resolveDashboardFilters on the backend).
  const dashDeviceOptions = devices
    .filter(d => dashDevices.includes(d.fromhost_ip))
    .map(d => ({ label: resolveDeviceDisplayName(d), value: d.fromhost_ip }))

  // Only offer sorting by a field the dashboard doesn't already pin to a
  // single value - "By Device" is meaningless when the dashboard is scoped
  // to exactly one device, but still useful with zero (all) or several.
  const sortOptions = [
    { label: 'Newest First', value: 'timestamp_desc' },
    { label: 'Oldest First', value: 'timestamp_asc' },
    ...(dashFixedSeverity ? [] : [{ label: 'By Severity', value: 'severity' }]),
    ...(dashDevices.length === 1 ? [] : [{ label: 'By Device', value: 'hostname' }]),
  ]

  const buildCustomColumns = () => {
    const cols = []
    if (fields.length > 0) {
      for (const field of fields) {
        cols.push({
          title: field,
          key: `pf_${field}`,
          width: 120,
          ellipsis: true,
          render: (_v: unknown, r: LogEntry) => {
            const val = r.parsed_fields?.[field]
            return val ? <Tag color="geekblue" style={{ maxWidth: 110, overflow: 'hidden', textOverflow: 'ellipsis' }}>{val}</Tag> : <Tag>-</Tag>
          },
        })
      }
    }
    return cols
  }

  const columns = [
    {
      title: 'Time',
      dataIndex: 'timestamp',
      key: 'timestamp',
      width: 180,
      render: (v: string) => new Date(v).toLocaleString(),
    },
    {
      title: 'Device',
      dataIndex: 'hostname',
      key: 'hostname',
      width: 150,
      render: (v: string, r: LogEntry) => <Tag color="blue">{resolveHostname(v, r.fromhost_ip, r.display_name)}</Tag>,
    },
    {
      title: 'Severity',
      dataIndex: 'severity',
      key: 'severity',
      width: 100,
      render: (v: string) => <SeverityTag severity={v} />,
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
      width: 300,
      ellipsis: { showTitle: true },
      render: (v: string, record: LogEntry) => {
        const display = record.raw_message || v
        return (
          <pre style={{
            margin: 0,
            width: '100%',
            maxWidth: '100%',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-all',
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
    ...buildCustomColumns(),
  ]

  const renderDetailContent = () => {
    if (!detailLog) return null
    const items: { label: string; content: React.ReactNode }[] = [
      { label: 'Timestamp', content: new Date(detailLog.timestamp).toLocaleString() },
      { label: 'Hostname', content: <Tag color="blue">{resolveHostname(detailLog.hostname, detailLog.fromhost_ip, detailLog.display_name)}</Tag> },
      { label: 'Source IP', content: detailLog.fromhost_ip ? <Tag color="green">{detailLog.fromhost_ip}</Tag> : '-' },
      { label: 'Severity', content: <SeverityTag severity={detailLog.severity} /> },
      { label: 'Facility', content: detailLog.facility ?? '-' },
      { label: 'App', content: detailLog.app_name ?? '-' },
      { label: 'Process ID', content: detailLog.process_id ?? '-' },
    ]

    if (fields.length > 0) {
      for (const field of fields) {
        const val = detailLog.parsed_fields?.[field]
        if (val) {
          items.push({
            label: field,
            content: <Tag color="geekblue" style={{ maxWidth: '100%', whiteSpace: 'normal', wordBreak: 'break-all' }}>{val}</Tag>,
          })
        }
      }
    }

    items.push({
      label: 'Matched Parsers',
      content: detailLog.matched_parsers && detailLog.matched_parsers.length > 0
        ? detailLog.matched_parsers.map(p => <Tag key={p} color="purple">{p}</Tag>)
        : 'None',
    })

    return {
      metadata: items,
      message: detailLog.raw_message || detailLog.message,
    }
  }

  if (loading) {
    return <Spin size="large" style={{ display: 'block', margin: '100px auto' }} />
  }

  if (!dashboard) {
    return <div>Dashboard not found</div>
  }

  return (
    <>
      <Breadcrumb style={{ marginBottom: 16 }}>
        <Breadcrumb.Item><a onClick={() => navigate('/dashboards')}>Dashboards</a></Breadcrumb.Item>
        <Breadcrumb.Item>{dashboard.name}</Breadcrumb.Item>
      </Breadcrumb>

      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 8, marginBottom: 16 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/dashboards')}>Back</Button>
          <Title level={3} style={{ margin: 0, whiteSpace: 'nowrap' }}>{dashboard.name}</Title>
          <Button
            icon={dashboard.pinned ? <PushpinFilled /> : <PushpinOutlined />}
            onClick={handleTogglePin}
            style={{ color: dashboard.pinned ? '#faad14' : undefined }}
          />
          {isOwner && <Button
            icon={<GlobalOutlined />}
            onClick={handleTogglePublic}
            type={dashboard.is_public ? 'primary' : 'default'}
          >
            {dashboard.is_public ? 'Public' : 'Private'}
          </Button>}
          {!isOwner && dashboard.owner_username && <Tag color="blue">by {dashboard.owner_username}</Tag>}
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
          <Input
            ref={searchRef}
            placeholder="Search... (Ctrl+K)"
            value={filters.search}
            onChange={e => setFilters(f => ({ ...f, search: e.target.value }))}
            style={{ minWidth: 180, flex: '1 1 180px' }}
            prefix={<FilterOutlined />}
          />
          <Select
            placeholder="Severity"
            allowClear
            style={{ minWidth: 140, flex: '1 1 140px' }}
            value={filters.severity || undefined}
            onChange={(v) => setFilters(f => ({ ...f, severity: v || '' }))}
            options={severities.map(s => ({ label: SEVERITY_LABELS[s] || s, value: s }))}
          />
          {dashDevices.length > 1 && (
            <Select
              placeholder="Device"
              allowClear
              style={{ minWidth: 140, flex: '1 1 140px' }}
              value={filters.fromhost_ip || undefined}
              onChange={(v) => setFilters(f => ({ ...f, fromhost_ip: v || '' }))}
              options={dashDeviceOptions}
            />
          )}
          <RangePicker
            style={{ minWidth: 260, flex: '1 1 260px' }}
            showTime
            value={filters.from && filters.to ? [dayjs(filters.from), dayjs(filters.to)] : null}
            onChange={(dates) => setFilters(f => ({
              ...f,
              from: (dates as [Dayjs | null, Dayjs | null] | null)?.[0]?.toISOString() || '',
              to: (dates as [Dayjs | null, Dayjs | null] | null)?.[1]?.toISOString() || '',
            }))}
          />
          <Select
            placeholder="Sort"
            style={{ minWidth: 130, flex: '1 1 130px' }}
            value={filters.sort}
            onChange={(v) => setFilters(f => ({ ...f, sort: v }))}
            options={sortOptions}
          />
          <Popconfirm title="Export as CSV?" onConfirm={() => handleExport('csv')}>
            <Button>CSV</Button>
          </Popconfirm>
          <Popconfirm title="Export as HTML?" onConfirm={() => handleExport('html')}>
            <Button>HTML</Button>
          </Popconfirm>
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
          {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>Reset</Button>}
        </div>
      </div>

      {dashboard.description && (
        <Typography.Paragraph type="secondary" style={{ marginBottom: 16 }}>{dashboard.description}</Typography.Paragraph>
      )}

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={12} md={8} lg={6}>
          <Card>
            <Statistic title="Total Logs" value={totalLogs} />
          </Card>
        </Col>
        <Col xs={24} sm={12} md={8} lg={6}>
          <Card>
            <Statistic title="Devices" value={dashDevices.length || 'All'} />
          </Card>
        </Col>
        <Col xs={24} sm={12} md={8} lg={6}>
          <Card>
            <Statistic title="Fields" value={fields.length || 'Default'} />
          </Card>
        </Col>
        <Col xs={24} sm={12} md={8} lg={6}>
          <Card>
            <Statistic title="Updated" value={new Date(dashboard.updated_at).toLocaleString()} />
          </Card>
        </Col>
      </Row>

      {(dashDevices.length > 0 || fields.length > 0) && (
        <Row gutter={16} style={{ marginBottom: 16 }}>
          <Col xs={24} md={8}>
            <Card size="small" title="Devices">
              {dashDevices.length ? dashDevices.map(d => <Tag key={d}>{d}</Tag>) : <Tag>All</Tag>}
            </Card>
          </Col>
          <Col xs={24} md={8}>
            <Card size="small" title="Active Fields">
              {fields.length ? fields.map(f => <Tag key={f} color="green">{f}</Tag>) : <Tag>Default</Tag>}
            </Card>
          </Col>
          <Col xs={24} md={8}>
            <Card size="small" title="Severity Filter">
              <Tag>{dashboard.config.filters.severity || 'All'}</Tag>
            </Card>
          </Col>
        </Row>
      )}

      <Table
        dataSource={logs}
        columns={enhanceColumns(columns)}
        rowKey="id"
        loading={tableLoading}
        size="small"
        tableLayout="fixed"
        scroll={{ x: 'max-content' }}
        onRow={(record) => ({
          onClick: () => setDetailLog(record),
          style: { cursor: 'pointer' },
        })}
        pagination={false}
      />
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', gap: 8, marginTop: 16 }}>
        <Select
          size="small"
          style={{ width: 100 }}
          value={pageSize}
          onChange={setPageSize}
          options={['25', '50', '100', '200'].map(v => ({ label: `${v} / page`, value: parseInt(v) }))}
        />
        {hasMore ? (
          <Button onClick={handleLoadMore} loading={loadingMore}>Load more</Button>
        ) : (
          <Text type="secondary">No more logs</Text>
        )}
      </div>

      <Modal
        title="Log Details"
        open={!!detailLog}
        onCancel={() => setDetailLog(null)}
        footer={[
          <Button key="close" onClick={() => setDetailLog(null)} style={{ width: '100%' }}>Close</Button>
        ]}
        width={{ sm: '90%', md: 720 }}
      >
        {detailLog && (() => {
          const detail = renderDetailContent()
          if (!detail) return null
          return (
            <Descriptions bordered column={1} size="small">
              {detail.metadata.map((item, i) => (
                <Descriptions.Item key={i} label={item.label}>
                  {item.content}
                </Descriptions.Item>
              ))}
              <Descriptions.Item label="Full Message">
                <pre style={{
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-all',
                  width: '100%',
                  maxWidth: '100%',
                  fontFamily: 'Consolas, Monaco, monospace',
                  fontSize: 12,
                  lineHeight: 1.4,
                  margin: 0,
                }}>
                  {detail.message}
                </pre>
              </Descriptions.Item>
            </Descriptions>
          )
        })()}
      </Modal>
    </>
  )
}