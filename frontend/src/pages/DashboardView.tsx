import { useEffect, useState, useCallback, useRef } from 'react'
import { Card, Table, Button, Tag, Space, Breadcrumb, Spin, Typography, Input, InputRef, Select, Row, Col, Statistic, Descriptions, Modal, DatePicker, Form, message } from 'antd'
import { ArrowLeftOutlined, ReloadOutlined, FilterOutlined, PushpinOutlined, PushpinFilled, RestOutlined, GlobalOutlined } from '@ant-design/icons'
import { useNavigate, useParams } from 'react-router-dom'
import { getDashboard, getDashboardData, togglePinDashboard, togglePublicDashboard, Dashboard, DashboardDataResponse, LogEntry } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'
import SeverityTag from '../components/SeverityTag'
import { SEVERITY_LABELS } from '../constants'
import { useAuth } from '../services/auth'

const { Title, Text } = Typography
const { RangePicker } = DatePicker

const severities = ['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug']

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
  const [dateRange, setDateRange] = useState<[any, any] | null>(null)
  const [detailLog, setDetailLog] = useState<LogEntry | null>(null)
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

  const loadLogs = useCallback(async () => {
    setTableLoading(true)
    setPage(1)
    try {
      const from = dateRange?.[0]?.toISOString() || ''
      const to = dateRange?.[1]?.toISOString() || ''
      const data = await getDashboardData(dashboardId, pageSize, 0, searchOverride, severityFilter, from, to)
      setLogs(data.logs)
      setTotal(data.total)
    } catch (e) {
      // error handled by API
    } finally {
      setTableLoading(false)
    }
  }, [dashboardId, pageSize, searchOverride, severityFilter, dateRange])

  useEffect(() => {
    loadDashboard()
  }, [dashboardId])

  useEffect(() => {
    if (dashboard) {
      loadLogs()
    }
  }, [dashboard, loadLogs])

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
  const devices = dashboard?.config?.devices || []

  const buildCustomColumns = (): any[] => {
    const cols: any[] = []
    if (fields.length > 0) {
      for (const field of fields) {
        cols.push({
          title: field,
          key: `pf_${field}`,
          width: 120,
          ellipsis: true,
          render: (_: any, r: LogEntry) => {
            const val = r.parsed_fields?.[field]
            return val ? <Tag color="geekblue" style={{ maxWidth: 110, overflow: 'hidden', textOverflow: 'ellipsis' }}>{val}</Tag> : <Tag>-</Tag>
          },
        })
      }
    }
    return cols
  }

  const columns: any[] = [
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
      render: (v: string) => <Tag color="blue">{v}</Tag>,
      filters: Array.from(new Set(logs.map(l => l.hostname))).map(h => ({ text: h, value: h })),
      onFilter: (v: any, record: LogEntry) => record.hostname === String(v),
    },
    {
      title: 'Severity',
      dataIndex: 'severity',
      key: 'severity',
      width: 100,
      render: (v: string) => <SeverityTag severity={v} />,
      filters: severities.map(s => ({ text: (SEVERITY_LABELS[s] || s).toUpperCase(), value: s })),
      onFilter: (v: any, record: LogEntry) => record.severity === String(v),
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
    const items: { label: string; content: React.ReactNode; span?: number }[] = [
      { label: 'Timestamp', content: new Date(detailLog.timestamp).toLocaleString() },
      { label: 'Hostname', content: <Tag color="blue">{detailLog.hostname}</Tag> },
      { label: 'Severity', content: <SeverityTag severity={detailLog.severity} /> },
      { label: 'Facility', content: detailLog.facility ?? '-' },
      { label: 'App', content: detailLog.app_name ?? '-' },
      { label: 'Process ID', content: detailLog.process_id ?? '-' },
    ]

    if (fields.length > 0) {
      for (const field of fields) {
        const val = detailLog.parsed_fields?.[field]
        items.push({ label: field, content: val ? <Tag color="geekblue">{val}</Tag> : '-', span: 1 })
      }
    }

    if (detailLog.matched_parsers && detailLog.matched_parsers.length > 0) {
      items.push({
        label: 'Matched Parsers',
        content: detailLog.matched_parsers.map(p => <Tag key={p} color="purple">{p}</Tag>),
      })
    }

    items.push({
      label: 'Full Message',
      content: (
        <pre style={{
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          fontFamily: 'Consolas, Monaco, monospace',
          fontSize: 12,
          lineHeight: 1.4,
          margin: 0,
        }}>
          {detailLog.raw_message || detailLog.message}
        </pre>
      ),
    })

    return items
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

      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 16 }}>
        <Space>
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/dashboards')}>Back</Button>
          <Title level={3} style={{ margin: 0 }}>{dashboard.name}</Title>
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
        </Space>
        <Space>
          <Input
            ref={searchRef}
            placeholder="Search... (Ctrl+K)"
            value={searchOverride}
            onChange={e => setSearchOverride(e.target.value)}
            onPressEnter={loadLogs}
            style={{ width: 180 }}
            prefix={<FilterOutlined />}
          />
          <Select
            placeholder="Severity"
            allowClear
            style={{ width: 140 }}
            value={severityFilter || undefined}
            onChange={(v) => setSeverityFilter(v || '')}
            options={severities.map(s => ({ label: SEVERITY_LABELS[s] || s, value: s }))}
          />
          <RangePicker
            style={{ width: 260 }}
            showTime
            value={dateRange}
            onChange={(dates) => setDateRange(dates as [any, any] | null)}
          />
          <Button icon={<ReloadOutlined />} onClick={loadLogs} loading={tableLoading}>Apply</Button>
          {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>Reset</Button>}
        </Space>
      </Space>

      {dashboard.description && (
        <Typography.Paragraph type="secondary" style={{ marginBottom: 16 }}>{dashboard.description}</Typography.Paragraph>
      )}

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <Card>
            <Statistic title="Matching Logs" value={total} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Devices" value={devices.length || 'All'} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Fields" value={fields.length || 'Default'} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Updated" value={new Date(dashboard.updated_at).toLocaleString()} />
          </Card>
        </Col>
      </Row>

      {(devices.length > 0 || fields.length > 0) && (
        <Descriptions bordered column={3} size="small" style={{ marginBottom: 16 }}>
          <Descriptions.Item label="Devices" span={1}>
            {devices.length ? devices.map(d => <Tag key={d}>{d}</Tag>) : <Tag>All</Tag>}
          </Descriptions.Item>
          <Descriptions.Item label="Active Fields" span={1}>
            {fields.length ? fields.map(f => <Tag key={f} color="green">{f}</Tag>) : <Tag>Default</Tag>}
          </Descriptions.Item>
          <Descriptions.Item label="Severity Filter" span={1}>
            <Tag>{dashboard.config.filters.severity || 'All'}</Tag>
          </Descriptions.Item>
        </Descriptions>
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
          onChange: (p, ps) => { setPage(p); setPageSize(ps) },
        }}
      />

      <Modal
        title="Log Details"
        open={!!detailLog}
        onCancel={() => setDetailLog(null)}
        footer={[
          <Button key="close" onClick={() => setDetailLog(null)}>Close</Button>
        ]}
        width={720}
      >
        {detailLog && (
          <Descriptions column={2} size="small" bordered>
            {(renderDetailContent() ?? []).map((item, i) => (
              <Descriptions.Item key={i} label={item.label} span={item.span || 1}>
                {item.content}
              </Descriptions.Item>
            ))}
          </Descriptions>
        )}
      </Modal>
    </>
  )
}