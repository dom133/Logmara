import { useEffect, useState } from 'react'
import { Row, Col, Card, Statistic, Table, Tag, Spin, Typography, Space, Button } from 'antd'
import { RestOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import { getDashboardStats, getTimeline, getSeverityStats } from '../services/api'
import { DashboardStats, TimelinePoint } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'

const { Title } = Typography

const severityColors: Record<string, string> = {
  emerg: '#f5222d',
  alert: '#ff4d4f',
  crit: '#ff7a45',
  err: '#faad14',
  warning: '#fadb14',
  notice: '#1890ff',
  info: '#52c41a',
  debug: '#bfbfbf',
}

export default function Dashboard() {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [timeline, setTimeline] = useState<TimelinePoint[]>([])
  const [severityData, setSeverityData] = useState<Array<{ severity: string; count: number }>>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    loadData()
    const interval = setInterval(loadData, 30000)
    return () => clearInterval(interval)
  }, [])

  const loadData = async () => {
    try {
      const [s, t, sv] = await Promise.all([
        getDashboardStats(),
        getTimeline('1h'),
        getSeverityStats(),
      ])
      setStats(s)
      setTimeline(t)
      setSeverityData(sv)
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
        itemStyle: { color: severityColors[s.severity] || '#bfbfbf' },
      })),
      label: { show: true, formatter: '{b}: {c}' },
    }],
  }

  if (loading && !stats) {
    return <Spin size="large" style={{ display: 'block', margin: '100px auto' }} />
  }

  const { enhanceColumns: enhanceDevices, hasChanges: devChanged, reset: resetDevices } = useColumnWidths(
    'col_widths_dashboard_devs',
    [{ key: 'hostname', width: 160 }, { key: 'count', width: 100 }],
  )

  const { enhanceColumns: enhanceErrors, hasChanges: errChanged, reset: resetErrors } = useColumnWidths(
    'col_widths_dashboard_errs',
    [{ key: 'message', width: 300 }, { key: 'hostname', width: 160 }, { key: 'count', width: 80 }],
  )

  const topDevicesColumns = [
    { title: 'Source IP', dataIndex: 'hostname', key: 'hostname', width: 160, render: (v: string) => <Tag color="blue">{v}</Tag> },
    { title: 'Logs', dataIndex: 'count', key: 'count', width: 100, sorter: (a: any, b: any) => a.count - b.count },
  ]

  const topErrorsColumns = [
    { title: 'Message', dataIndex: 'message', key: 'message', width: 300, ellipsis: true },
    { title: 'Source', dataIndex: 'hostname', key: 'hostname', width: 160, render: (v: string) => <Tag>{v}</Tag> },
    { title: 'Count', dataIndex: 'count', key: 'count', width: 80 },
  ]

  return (
    <>
      <Title level={3}>Dashboard</Title>
      <Row gutter={16} style={{ marginBottom: 24 }}>
        <Col span={6}>
          <Card>
            <Statistic title="Total Logs" value={stats?.total_logs || 0} prefix="📄" />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Last Hour" value={stats?.logs_last_hour || 0} valueStyle={{ color: '#3f8600' }} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Last 24h" value={stats?.logs_last_day || 0} valueStyle={{ color: '#cf1322' }} />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic title="Devices" value={stats?.unique_devices || 0} prefix="🖥️" />
          </Card>
        </Col>
      </Row>

      <Row gutter={16} style={{ marginBottom: 24 }}>
        <Col span={14}>
          <Card title="Logs Timeline (Last 24h)">
            <ReactECharts option={timelineOption} style={{ height: 300 }} />
          </Card>
        </Col>
        <Col span={10}>
          <Card title="Severity Distribution">
            <ReactECharts option={severityOption} style={{ height: 300 }} />
          </Card>
        </Col>
      </Row>

      <Row gutter={16}>
        <Col span={12}>
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
            />
          </Card>
        </Col>
        <Col span={12}>
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
            />
          </Card>
        </Col>
      </Row>
    </>
  )
}