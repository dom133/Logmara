import { useEffect, useState, useMemo } from 'react'
import { Row, Col, Card, Table, Tag, Spin, Typography, Button } from 'antd'
import { RestOutlined, FileTextOutlined, ClockCircleOutlined, CalendarOutlined, DesktopOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import { getDashboardStats, getTimeline, getSeverityStats, getDevices, DeviceStats, resolveDeviceDisplayName } from '../services/api'
import { DashboardStats, TimelinePoint } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'
import StatCard from '../components/StatCard'
import { SEVERITY_COLORS } from '../constants'

const { Title } = Typography

export default function Dashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [timeline, setTimeline] = useState<TimelinePoint[]>([])
  const [severityData, setSeverityData] = useState<Array<{ severity: string; count: number }>>([])
  const [loading, setLoading] = useState(true)
  const [devices, setDevices] = useState<DeviceStats[]>([])

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

  const { enhanceColumns: enhanceDevices, hasChanges: devChanged, reset: resetDevices } = useColumnWidths(
    'col_widths_dashboard_devs',
    [{ key: 'hostname', width: 160 }, { key: 'count', width: 100 }],
  )

  const { enhanceColumns: enhanceErrors, hasChanges: errChanged, reset: resetErrors } = useColumnWidths(
    'col_widths_dashboard_errs',
    [{ key: 'message', width: 140 }, { key: 'hostname', width: 120 }, { key: 'count', width: 70 }],
  )

  useEffect(() => {
    loadData()
    const interval = setInterval(loadData, 30000)
    return () => clearInterval(interval)
  }, [])

  const loadDevices = async () => {
    const d = await getDevices()
    setDevices(d)
  }

  const loadData = async () => {
    try {
      const [s, t, sv, dv] = await Promise.all([
        getDashboardStats(),
        getTimeline('1h'),
        getSeverityStats(),
        getDevices(),
      ])
      setStats(s)
      setTimeline(t)
      setSeverityData(sv)
      setDevices(dv)
    } finally {
      setLoading(false)
    }
  }

  const timelineOption = {
    tooltip: { trigger: 'axis' as const },
    xAxis: {
      type: 'category' as const,
      data: timeline.map(p => new Date(p.timestamp).toLocaleTimeString()),
    },
    yAxis: { type: 'value' as const },
    series: [{
      data: timeline.map(p => p.count),
      type: 'line' as const,
      smooth: true,
      areaStyle: { opacity: 0.2 },
      lineStyle: { width: 2 },
      itemStyle: { color: '#1890ff' },
    }],
    grid: { left: 50, right: 20, top: 30, bottom: 30 },
  }

  const severityOption = {
    tooltip: { trigger: 'item' as const },
    legend: { orient: 'vertical' as const, left: 'left' as const, top: 'middle' as const },
    series: [{
      type: 'pie' as const,
      radius: ['40%', '70%'],
      avoidLabelOverlap: false,
      data: severityData.map(s => ({
        name: s.severity.toUpperCase(),
        value: s.count,
        itemStyle: { color: SEVERITY_COLORS[s.severity] || '#bfbfbf' },
      })),
      label: { show: true, formatter: '{b}: {c}' },
    }],
  }

  if (loading && !stats) {
    return <Spin size="large" style={{ display: 'block', margin: '100px auto' }} />
  }

  const topDevicesColumns = [
    { title: 'Device', dataIndex: 'hostname', key: 'hostname', width: 160, render: (v: string, record: any) => (
      <a onClick={() => window.location.href = `/logs?fromhost_ip=${encodeURIComponent(record.fromhost_ip)}`}><Tag color="blue">{resolveHostname(v, record.fromhost_ip)}</Tag></a>
    )},
    { title: 'Logs', dataIndex: 'count', key: 'count', width: 100, sorter: (a: any, b: any) => a.count - b.count },
  ]

  const topErrorsColumns = [
    { title: 'Message', dataIndex: 'message', key: 'message', width: 140, ellipsis: true },
    { title: 'Source', dataIndex: 'hostname', key: 'hostname', width: 120, render: (v: string, record: any) => (
      <a onClick={() => window.location.href = `/logs?fromhost_ip=${encodeURIComponent(record.fromhost_ip)}`}><Tag color="blue">{resolveHostname(v, record.fromhost_ip)}</Tag></a>
    )},
    { title: 'Count', dataIndex: 'count', key: 'count', width: 70 },
  ]

  return (
    <>
      <Title level={3}>Dashboard</Title>
      <Row gutter={16} style={{ marginBottom: 24 }}>
        <Col xs={24} sm={12} md={6}>
          <StatCard title="Total Logs" value={stats?.total_logs || 0} icon={<FileTextOutlined />} color="#1890ff" />
        </Col>
        <Col xs={24} sm={12} md={6}>
          <StatCard title="Last Hour" value={stats?.logs_last_hour || 0} icon={<ClockCircleOutlined />} color="#3f8600" />
        </Col>
        <Col xs={24} sm={12} md={6}>
          <StatCard title="Last 24h" value={stats?.logs_last_day || 0} icon={<CalendarOutlined />} color="#cf1322" />
        </Col>
        <Col xs={24} sm={12} md={6}>
          <StatCard title="Devices" value={stats?.unique_devices || 0} icon={<DesktopOutlined />} color="#722ed1" />
        </Col>
      </Row>

      <Row gutter={16} style={{ marginBottom: 24 }}>
        <Col xs={24} lg={14}>
          <Card title="Logs Timeline (Last 24h)">
            <ReactECharts option={timelineOption} style={{ height: 300 }} />
          </Card>
        </Col>
        <Col xs={24} lg={10}>
          <Card title="Severity Distribution">
            <ReactECharts option={severityOption} style={{ height: 300 }} />
          </Card>
        </Col>
      </Row>

      <Row gutter={16}>
        <Col xs={24} md={12}>
          <Card
            title="Top Devices"
            extra={devChanged ? <Button size="small" icon={<RestOutlined />} onClick={resetDevices}>Reset</Button> : undefined}
          >
            <Table
              dataSource={stats?.top_devices || []}
              columns={enhanceDevices(topDevicesColumns)}
              rowKey="hostname"
              pagination={false}
              size="small"
              scroll={{ x: 'max-content' }}
            />
          </Card>
        </Col>
        <Col xs={24} md={12}>
          <Card
            title="Top Errors"
            extra={errChanged ? <Button size="small" icon={<RestOutlined />} onClick={resetErrors}>Reset</Button> : undefined}
          >
            <Table
              dataSource={stats?.top_errors || []}
              columns={enhanceErrors(topErrorsColumns)}
              rowKey={(r, i) => (i ?? 0).toString()}
              pagination={false}
              size="small"
              scroll={{ x: 'max-content' }}
            />
          </Card>
        </Col>
      </Row>
    </>
  )
}