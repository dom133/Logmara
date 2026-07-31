import { useEffect, useState, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Row, Col, Card, Table, Tag, Spin, Typography, Button } from 'antd'
import { RestOutlined, FileTextOutlined, ClockCircleOutlined, CalendarOutlined, DesktopOutlined, ThunderboltOutlined } from '@ant-design/icons'
import ReactECharts from 'echarts-for-react'
import { getDashboardStats, getTimeline, getSeverityStats, getDevices, getLogsRate, DeviceStats, resolveDeviceDisplayName } from '../services/api'
import { DashboardStats, TimelinePoint } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'
import StatCard from '../components/StatCard'
import { SEVERITY_HEX, getSeverityLabels, SEVERITY_ORDER } from '../constants'

const { Title } = Typography

export default function Dashboard() {
  const { t } = useTranslation()
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [timeline, setTimeline] = useState<TimelinePoint[]>([])
  const [severityData, setSeverityData] = useState<Array<{ severity: string; count: number }>>([])
  const [loading, setLoading] = useState(true)
  const [devices, setDevices] = useState<DeviceStats[]>([])
  const [logsPerSec, setLogsPerSec] = useState(0)

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

  const [isTabActive, setIsTabActive] = useState(true)

  useEffect(() => {
    // Check if tab is active
    const handleVisibilityChange = () => {
      setIsTabActive(!document.hidden)
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    loadData()
    const interval = setInterval(() => {
      if (isTabActive) {
        loadData()
      }
    }, 30000)

    return () => {
      clearInterval(interval)
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [isTabActive])

  // Polled separately, and much faster than the 30s loadData() cycle above -
  // this is the one dashboard number meant to look "live", and piggybacking
  // it on the heavier stats/timeline/severity/devices refresh would either
  // make it stale or force everything else to poll unnecessarily often.
  useEffect(() => {
    const loadRate = async () => {
      try {
        setLogsPerSec(await getLogsRate())
      } catch {
        // A missed rate tick isn't worth its own error UI; the next tick retries.
      }
    }
    loadRate()
    const interval = setInterval(() => {
      if (isTabActive) {
        loadRate()
      }
    }, 4000)

    return () => clearInterval(interval)
  }, [isTabActive])

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

  const severityRank = (severity: string) => {
    const idx = SEVERITY_ORDER.indexOf(severity)
    return idx === -1 ? SEVERITY_ORDER.length : idx
  }
  const sortedSeverity = [...severityData]
    .filter(s => s.count > 0)
    .sort((a, b) => severityRank(a.severity) - severityRank(b.severity))
  const totalSeverity = sortedSeverity.reduce((sum, s) => sum + s.count, 0)

  const severityOption = {
    tooltip: { trigger: 'item' as const, formatter: '{b}: {c} ({d}%)' },
    legend: {
      orient: 'horizontal' as const,
      type: 'scroll' as const,
      bottom: 0,
      left: 'center' as const,
    },
    title: {
      text: totalSeverity.toLocaleString(),
      subtext: t('dashboard.total'),
      left: 'center' as const,
      top: '38%' as const,
      textStyle: { fontSize: 22, fontWeight: 600 },
      subtextStyle: { fontSize: 12 },
    },
    series: [{
      type: 'pie' as const,
      radius: ['45%', '68%'],
      center: ['50%', '44%'],
      avoidLabelOverlap: true,
      data: sortedSeverity.map(s => ({
        name: getSeverityLabels(t)[s.severity] || s.severity,
        value: s.count,
        itemStyle: { color: SEVERITY_HEX[s.severity] || '#bfbfbf' },
      })),
      label: { show: true, formatter: '{b}: {d}%' },
      labelLine: { show: true },
    }],
  }

  if (loading && !stats) {
    return <Spin size="large" style={{ display: 'block', margin: '100px auto' }} />
  }

  const topDevicesColumns = [
    { title: t('dashboard.device'), dataIndex: 'hostname', key: 'hostname', width: 160, render: (v: string, record: DeviceStats) => (
      <a onClick={() => window.location.href = `/logs?fromhost_ip=${encodeURIComponent(record.fromhost_ip)}`}><Tag color="blue">{resolveHostname(v, record.fromhost_ip)}</Tag></a>
    )},
    { title: t('dashboard.logs'), dataIndex: 'total_logs', key: 'total_logs', width: 100, sorter: (a: DeviceStats, b: DeviceStats) => a.total_logs - b.total_logs },
  ]

  const topErrorsColumns = [
    { title: t('dashboard.message'), dataIndex: 'message', key: 'message', width: 140, ellipsis: true },
    { title: t('dashboard.source'), dataIndex: 'hostname', key: 'hostname', width: 120, render: (v: string, record: { hostname: string; fromhost_ip?: string; count: number }) => (
      <a onClick={() => window.location.href = `/logs?fromhost_ip=${encodeURIComponent(record.fromhost_ip || '')}`}><Tag color="blue">{resolveHostname(v, record.fromhost_ip)}</Tag></a>
    )},
    { title: t('dashboard.count'), dataIndex: 'count', key: 'count', width: 70 },
  ]

  const fmt = (n: number) => n.toLocaleString('pl-PL')
  const statTiles = [
    { title: t('dashboard.totalLogs'), value: fmt(stats?.total_logs || 0), icon: <FileTextOutlined />, color: '#1890ff' },
    { title: t('dashboard.lastHour'), value: fmt(stats?.logs_last_hour || 0), icon: <ClockCircleOutlined />, color: '#3f8600' },
    { title: t('dashboard.last24h'), value: fmt(stats?.logs_last_day || 0), icon: <CalendarOutlined />, color: '#cf1322' },
    { title: t('dashboard.logsPerSec'), value: logsPerSec.toFixed(1), subtitle: t('dashboard.avgLast10s'), icon: <ThunderboltOutlined />, color: '#13c2c2' },
    { title: t('dashboard.devices'), value: fmt(stats?.unique_devices || 0), icon: <DesktopOutlined />, color: '#722ed1' },
  ]

  return (
    <>
      <Title level={3}>{t('dashboard.title')}</Title>
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))',
        columnGap: 16,
        rowGap: 24,
        marginBottom: 24,
      }}>
        {statTiles.map(c => (
          <StatCard key={c.title} title={c.title} value={c.value} subtitle={c.subtitle} icon={c.icon} color={c.color} />
        ))}
      </div>

      <Row gutter={16} style={{ marginBottom: 24 }}>
        <Col xs={24} lg={14}>
          <Card title={t('dashboard.logsTimeline')}>
            <ReactECharts option={timelineOption} style={{ height: 300 }} />
          </Card>
        </Col>
        <Col xs={24} lg={10}>
          <Card title={t('dashboard.severityDistribution')}>
            <ReactECharts option={severityOption} style={{ height: 340 }} />
          </Card>
        </Col>
      </Row>

      <Row gutter={16}>
        <Col xs={24} md={12}>
          <Card
            title={t('dashboard.topDevices')}
            extra={devChanged ? <Button size="small" icon={<RestOutlined />} onClick={resetDevices}>{t('common.reset')}</Button> : undefined}
          >
            <Table
              dataSource={stats?.top_devices || []}
              columns={enhanceDevices(topDevicesColumns)}
              rowKey="fromhost_ip"
              pagination={false}
              size="small"
              scroll={{ x: 'max-content' }}
            />
          </Card>
        </Col>
        <Col xs={24} md={12}>
          <Card
            title={t('dashboard.topErrors')}
            extra={errChanged ? <Button size="small" icon={<RestOutlined />} onClick={resetErrors}>{t('common.reset')}</Button> : undefined}
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