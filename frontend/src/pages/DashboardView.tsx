import { useEffect, useState, useCallback, useRef, useMemo } from 'react'
import { Card, Table, Button, Tag, Space, Breadcrumb, Spin, Typography, Input, InputRef, Select, Row, Col, Statistic, Descriptions, Modal, DatePicker, Form, message } from 'antd'
import { ArrowLeftOutlined, ReloadOutlined, FilterOutlined, PushpinOutlined, PushpinFilled, RestOutlined, GlobalOutlined, ClockCircleOutlined, UnorderedListOutlined } from '@ant-design/icons'
import { useNavigate, useParams } from 'react-router-dom'
import { getDashboard, getDashboardData, togglePinDashboard, togglePublicDashboard, Dashboard, DashboardDataResponse, LogEntry, getDevices, DeviceStats, resolveDeviceDisplayName } from '../services/api'
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
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [tableLoading, setTableLoading] = useState(false)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [searchOverride, setSearchOverride] = useState('')
  const [severityFilter, setSeverityFilter] = useState('')
  const searchOverrideRef = useRef(searchOverride)
  const severityRef = useRef(severityFilter)
  useEffect(() => { searchOverrideRef.current = searchOverride }, [searchOverride])
  useEffect(() => { severityRef.current = severityFilter }, [severityFilter])
  const [dateRange, setDateRange] = useState<[any, any] | null>(null)
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

  const loadLogs = useCallback(async (offset: number) => {
    setTableLoading(true)
    try {
      const from = dateRange?.[0]?.toISOString() || ''
      const to = dateRange?.[1]?.toISOString() || ''
      const data = await getDashboardData(dashboardId, pageSize, offset, searchOverrideRef.current, severityRef.current, from, to)
      setLogs(data.logs)
      setTotal(data.total)
    } catch (e) {
      // error handled by API
    } finally {
      setTableLoading(false)
    }
  }, [dashboardId, pageSize, dateRange])

  const pollLogs = useCallback(async () => {
    if (!appendMode) return
    try {
      const from = dateRange?.[0]?.toISOString() || ''
      const to = dateRange?.[1]?.toISOString() || ''
      const offset = (page - 1) * pageSize
      const data = await getDashboardData(dashboardId, pageSize, offset, searchOverrideRef.current, severityRef.current, from, to)
      setTotal(data.total)
      setLogs(data.logs)
    } catch (e) {
      // error handled by API
    }
  }, [dashboardId, pageSize, page, dateRange, appendMode])

  useEffect(() => {
    loadDashboard()
    loadDevices()
    const interval = setInterval(() => {
      if (isTabActive) {
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
  }, [dashboardId, isTabActive])

  useEffect(() => {
    if (dashboard) {
      loadLogs(0)
    }
  }, [dashboard, loadLogs])

  useEffect(() => {
    localStorage.setItem(`dashboard_refresh_interval_${dashboardId}`, refreshInterval.toString())
  }, [refreshInterval, dashboardId])

  useEffect(() => {
    if (!isTabActive) return
    const interval = setInterval(() => {
      pollLogs()
    }, refreshInterval * 1000)
    return () => clearInterval(interval)
  }, [isTabActive, pollLogs, refreshInterval])

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
      sorter: (a: LogEntry, b: LogEntry) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
      defaultSortOrder: 'descend',
    },
    {
      title: 'Device',
      dataIndex: 'hostname',
      key: 'hostname',
      width: 150,
      render: (v: string, r: LogEntry) => <Tag color="blue">{resolveHostname(v, r.fromhost_ip, r.display_name)}</Tag>,
      filters: Array.from(new Set(logs.map(l => l.hostname))).map(h => ({ text: h, value: h })),
      onFilter: (v: string, record: LogEntry) => record.hostname === String(v),
    },
    {
      title: 'Severity',
      dataIndex: 'severity',
      key: 'severity',
      width: 100,
      render: (v: string) => <SeverityTag severity={v} />,
      filters: severities.map(s => ({ text: (SEVERITY_LABELS[s] || s).toUpperCase(), value: s })),
      onFilter: (v: string, record: LogEntry) => record.severity === String(v),
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
        items.push({ label: field, content: val ? <Tag color="geekblue">{val}</Tag> : '-' })
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
            value={searchOverride}
            onChange={e => setSearchOverride(e.target.value)}
            onPressEnter={() => loadLogs((page - 1) * pageSize)}
            style={{ minWidth: 180, flex: '1 1 180px' }}
            prefix={<FilterOutlined />}
          />
          <Select
            placeholder="Severity"
            allowClear
            style={{ minWidth: 140, flex: '1 1 140px' }}
            value={severityFilter || undefined}
            onChange={(v) => setSeverityFilter(v || '')}
            options={severities.map(s => ({ label: SEVERITY_LABELS[s] || s, value: s }))}
          />
          <RangePicker
            style={{ minWidth: 260, flex: '1 1 260px' }}
            showTime
            value={dateRange}
            onChange={(dates) => setDateRange(dates as [any, any] | null)}
          />
          <Button icon={<ReloadOutlined />} onClick={() => loadLogs((page - 1) * pageSize)} loading={tableLoading}>Apply</Button>
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
            <Statistic title="Matching Logs" value={total} />
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
        scroll={{ x: 'max-content' }}
        onRow={(record) => ({
          onClick: () => setDetailLog(record),
          style: { cursor: 'pointer' },
        })}
        pagination={{
          current: page,
          pageSize: pageSize,
          total: total,
          showSizeChanger: true,
          showTotal: (t) => `${t} total`,
          onChange: (p, ps) => { setPage(p); setPageSize(ps); loadLogs((p - 1) * ps) },
        }}
      />

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
                  wordBreak: 'break-word',
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