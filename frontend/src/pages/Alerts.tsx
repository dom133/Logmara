import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Card, Table, Button, Tag, Space, message, Popconfirm, Tabs, Typography, Form } from 'antd'
import { PlusOutlined, DeleteOutlined, EditOutlined, ExperimentOutlined, EyeOutlined } from '@ant-design/icons'
import {
  getAlerts, createAlert, updateAlert, deleteAlert, Alert, AlertRequest, AlertRuleType,
  getNotificationChannels, createNotificationChannel, updateNotificationChannel, deleteNotificationChannel, testNotificationChannel,
  NotificationChannel, NotificationChannelRequest, NotificationChannelType,
  getNotificationHistory, clearNotificationHistory, NotificationLogEntry,
  getDevices, DeviceStats, getParsers, Parser, getParsedFields, ParsedField,
  getUserDirectory, UserSummary,
} from '../services/api'
import { useAuth } from '../services/auth'
import { onLiveNotification } from '../services/notificationEvents'
import { getErrorMessage } from '../utils/error'
import AlertFormModal from '../components/AlertFormModal'
import ChannelFormModal from '../components/ChannelFormModal'
import NotificationDetailModal from '../components/NotificationDetailModal'
import { ruleTypeLabels, channelTypeLabels, historyStatusColor } from '../constants/alertConstants'
import { HistoryGroup, groupHistoryEntries } from '../components/historyTypes'

const { Title, Text } = Typography

function RulesTab({ canEdit, isAdmin, active }: { canEdit: boolean; isAdmin: boolean; active: boolean }) {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [devices, setDevices] = useState<DeviceStats[]>([])
  const [parsers, setParsers] = useState<Parser[]>([])
  const [parsedFields, setParsedFields] = useState<ParsedField[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Alert | null>(null)
  const [form] = Form.useForm()

  const loadData = async () => {
    setLoading(true)
    try {
      const [a, c, d, p] = await Promise.all([getAlerts(), getNotificationChannels(), getDevices(), getParsers()])
      setAlerts(a)
      setChannels(c)
      setDevices(d)
      setParsers(p)
    } catch {
      message.error('Failed to load alerts')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [])

  useEffect(() => {
    if (!active) return
    return onLiveNotification(() => { loadData() })
  }, [active])

  const selectedDevices: string[] = Form.useWatch('device_ips', form) || []
  const selectedDevicesKey = selectedDevices.join(',')

  // Refetch the field registry scoped to the selected device(s) whenever
  // that selection changes, so "Field Conditions" only offers fields that
  // parsers have actually extracted from those devices' logs (falls back to
  // every known field when no device is selected).
  useEffect(() => {
    getParsedFields(selectedDevices.length > 0 ? selectedDevices : undefined)
      .then(setParsedFields)
      .catch(() => { /* keep the previous list rather than blanking the form */ })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedDevicesKey])

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({
      rule_type: 'log_threshold', is_active: true, window_minutes: 5, cooldown_minutes: 15, threshold: 5, fire_on_every_match: false,
      channel_ids: [], device_ips: [], parser_names: [], field_conditions: [], field_conditions_logic: 'and',
    })
    setModalOpen(true)
  }

  const openEdit = (a: Alert) => {
    setEditing(a)
    form.resetFields()
    form.setFieldsValue(a)
    setModalOpen(true)
  }

  const handleSubmit = async () => {
    const values = await form.validateFields() as AlertRequest
    try {
      if (editing) {
        await updateAlert(editing.id, values)
        message.success('Alert rule updated')
      } else {
        await createAlert(values)
        message.success('Alert rule created')
      }
      setModalOpen(false)
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to save alert rule'))
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteAlert(id)
      message.success('Alert rule deleted')
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to delete alert rule'))
    }
  }

  const channelName = (id: number) => channels.find(c => c.id === id)?.name || `#${id}`

  const columns = [
    { title: 'Name', dataIndex: 'name', key: 'name', render: (v: string, r: Alert) => <Space>{v}{!r.is_active && <Tag>Disabled</Tag>}</Space> },
    { title: 'Type', dataIndex: 'rule_type', key: 'rule_type', render: (v: AlertRuleType) => <Tag color="blue">{ruleTypeLabels[v]}</Tag> },
    {
      title: 'Condition', key: 'condition', ellipsis: true,
      render: (_v: unknown, r: Alert) => {
        const scope = [
          (r.device_ips || []).length > 0 ? `${r.device_ips.length} device(s)` : 'all devices',
          (r.parser_names || []).length > 0 ? `${r.parser_names.length} parser(s)` : null,
          (r.field_conditions || []).length > 0 ? `${r.field_conditions.length} field condition(s)` : null,
        ].filter(Boolean).join(', ')
        if (r.rule_type === 'log_threshold') {
          return `${r.threshold}+ matches / ${r.window_minutes}m on ${scope}${r.severity ? `, severity >= ${r.severity}` : ''}`
        }
        if (r.rule_type === 'device_silence') {
          return `silent for ${r.threshold}m on ${scope}`
        }
        if (r.rule_type === 'relay_cert_expiring') {
          return `warn ${r.threshold || 30} day(s) before a relay certificate expires`
        }
        if (r.rule_type === 'malformed_json') {
          return r.fire_on_every_match ? 'every malformed JSON line during ingestion' : 'any malformed JSON line during ingestion'
        }
        return r.audit_action_filter ? `action = ${r.audit_action_filter}` : 'any audit action'
      },
    },
    {
      title: 'Channels', key: 'channels',
      render: (_v: unknown, r: Alert) => (r.channel_ids || []).map(id => <Tag key={id}>{channelName(id)}</Tag>),
    },
    { title: 'Last Fired', dataIndex: 'last_fired_at', key: 'last_fired_at', render: (v?: string) => v ? new Date(v).toLocaleString() : '-' },
    {
      title: 'Actions', key: 'actions',
      render: (_v: unknown, r: Alert) => (
        <Space>
          {canEdit && <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />}
          {canEdit && <Popconfirm title="Delete alert rule?" onConfirm={() => handleDelete(r.id)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>}
        </Space>
      ),
    },
  ]

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        {canEdit && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>New Alert Rule</Button>}
      </div>
      <Table dataSource={alerts} columns={columns} rowKey="id" loading={loading} size="small" scroll={{ x: 'max-content' }} />

      <AlertFormModal
        open={modalOpen}
        editing={editing}
        isAdmin={isAdmin}
        devices={devices}
        parsers={parsers}
        parsedFields={parsedFields}
        channels={channels}
        form={form}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
      />
    </>
  )
}

function ChannelsTab({ canManage, isAdmin, currentUserId }: { canManage: boolean; isAdmin: boolean; currentUserId?: number }) {
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [users, setUsers] = useState<UserSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<NotificationChannel | null>(null)
  const [testingId, setTestingId] = useState<number | null>(null)
  const [form] = Form.useForm()

  const loadData = async () => {
    setLoading(true)
    try {
      setChannels(await getNotificationChannels())
      if (canManage) {
        try {
          setUsers(await getUserDirectory())
        } catch { /* picker shows no options */ }
      }
    } catch {
      message.error('Failed to load notification channels')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [canManage])

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ type: 'email', enabled: true })
    setModalOpen(true)
  }

  const openEdit = (c: NotificationChannel) => {
    setEditing(c)
    form.resetFields()
    form.setFieldsValue({
      name: c.name,
      type: c.type,
      enabled: c.enabled,
      to: (c.config?.to as string[] | undefined) || [],
      url: c.config?.url as string | undefined,
      webhook_url: c.config?.webhook_url as string | undefined,
      user_ids: (c.config?.user_ids as number[] | undefined) || [],
    })
    setModalOpen(true)
  }

  const handleSubmit = async () => {
    const values = await form.validateFields()
    const config: Record<string, unknown> = {}
    if (values.type === 'email') config.to = values.to || []
    if (values.type === 'webhook') config.url = values.url
    if (values.type === 'slack' || values.type === 'teams') config.webhook_url = values.webhook_url
    if (values.type === 'in_app' || values.type === 'push') config.user_ids = values.user_ids || []

    const req: NotificationChannelRequest = {
      name: values.name,
      type: values.type,
      enabled: values.enabled,
      config,
      secret: values.secret || undefined,
    }

    try {
      if (editing) {
        await updateNotificationChannel(editing.id, req)
        message.success('Channel updated')
      } else {
        await createNotificationChannel(req)
        message.success('Channel created')
      }
      setModalOpen(false)
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to save channel'))
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteNotificationChannel(id)
      message.success('Channel deleted')
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to delete channel'))
    }
  }

  const handleTest = async (id: number) => {
    setTestingId(id)
    try {
      await testNotificationChannel(id)
      message.success('Test notification sent')
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Test notification failed'))
    } finally {
      setTestingId(null)
    }
  }

  const columns = [
    { title: 'Name', dataIndex: 'name', key: 'name' },
    { title: 'Type', dataIndex: 'type', key: 'type', render: (v: NotificationChannelType) => <Tag color="blue">{channelTypeLabels[v]}</Tag> },
    { title: 'Status', dataIndex: 'enabled', key: 'enabled', render: (v: boolean) => <Tag color={v ? 'green' : 'red'}>{v ? 'Enabled' : 'Disabled'}</Tag> },
    {
      title: 'Owner', dataIndex: 'created_by_username', key: 'created_by_username',
      render: (username: string | undefined) => {
        if (username) return username
        return <Tag color="default">No owner</Tag>
      },
    },
    {
      title: 'Actions', key: 'actions',
      render: (_v: unknown, r: NotificationChannel) => {
        const canEditRow = r.created_by != null ? canManage && r.created_by === currentUserId : isAdmin
        return (
          <Space>
            <Button size="small" icon={<ExperimentOutlined />} loading={testingId === r.id} onClick={() => handleTest(r.id)}>Test</Button>
            {canEditRow && <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />}
            {canEditRow && <Popconfirm title="Delete channel?" onConfirm={() => handleDelete(r.id)}>
              <Button size="small" danger icon={<DeleteOutlined />} />
            </Popconfirm>}
          </Space>
        )
      },
    },
  ]

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        {canManage && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>New Channel</Button>}
      </div>
      <Table dataSource={channels} columns={columns} rowKey="id" loading={loading} size="small" scroll={{ x: 'max-content' }} />

      <ChannelFormModal
        open={modalOpen}
        editing={editing}
        users={users}
        form={form}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
      />
    </>
  )
}

function HistoryTab({ isAdmin, active, focusInAppId, focusFiringId, onFocusConsumed }: { isAdmin: boolean; active: boolean; focusInAppId?: number; focusFiringId?: string; onFocusConsumed: () => void }) {
  const [entries, setEntries] = useState<NotificationLogEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [clearing, setClearing] = useState(false)
  const [viewing, setViewing] = useState<HistoryGroup | null>(null)

  const loadData = () => {
    setLoading(true)
    getNotificationHistory().then(setEntries).catch(() => message.error('Failed to load notification history')).finally(() => setLoading(false))
  }

  useEffect(() => { loadData() }, [])

  const groups = groupHistoryEntries(entries)

  useEffect(() => {
    if (!active) return
    return onLiveNotification(() => { loadData() })
  }, [active])

  useEffect(() => {
    if ((!focusInAppId && !focusFiringId) || loading) return
    const match = focusFiringId
      ? groups.find((g) => g.key === focusFiringId)
      : groups.find((g) => g.channels.some((c) => c.in_app_notification_id === focusInAppId))
    if (match) {
      setViewing(match)
    } else {
      message.info('No matching history entry was found for that notification (it may be too old, or a test notification, which is never recorded in history).')
    }
    onFocusConsumed()
  }, [focusInAppId, focusFiringId, loading, entries, onFocusConsumed])

  const handleClear = async () => {
    setClearing(true)
    try {
      await clearNotificationHistory()
      message.success('Notification history cleared')
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to clear notification history'))
    } finally {
      setClearing(false)
    }
  }

  const columns = [
    { title: 'Time', dataIndex: 'createdAt', key: 'createdAt', render: (v: string) => new Date(v).toLocaleString() },
    { title: 'Alert', dataIndex: 'alertName', key: 'alertName' },
    {
      title: 'Channels', key: 'channels',
      render: (_v: unknown, g: HistoryGroup) => (
        <Space size={4} wrap>
          {g.channels.map((c) => (
            <Tag key={c.id} color={historyStatusColor[c.status] || 'default'}>{c.channel_name}</Tag>
          ))}
        </Space>
      ),
    },
    {
      title: 'Actions', key: 'actions',
      render: (_v: unknown, g: HistoryGroup) => (
        <Button size="small" icon={<EyeOutlined />} onClick={() => setViewing(g)}>Details</Button>
      ),
    },
  ]

  return (
    <>
      {isAdmin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
          <Popconfirm title="Clear all notification history?" onConfirm={handleClear}>
            <Button danger loading={clearing} disabled={entries.length === 0}>Clear History</Button>
          </Popconfirm>
        </div>
      )}
      <Table
        dataSource={groups}
        columns={columns}
        rowKey="key"
        loading={loading}
        size="small"
        scroll={{ x: 'max-content' }}
        onRow={(g) => ({ onClick: () => setViewing(g), style: { cursor: 'pointer' } })}
      />

      <NotificationDetailModal viewing={viewing} onClose={() => setViewing(null)} />
    </>
  )
}

export default function AlertsPage() {
  const { user } = useAuth()
  const canEdit = user?.role === 'admin' || user?.role === 'editor'
  const isAdmin = user?.role === 'admin'

  const [searchParams] = useSearchParams()
  const initialTab = searchParams.get('tab') === 'history' ? 'history' : 'rules'
  const initialFocusID = Number(searchParams.get('notification')) || undefined
  const initialFocusFiringID = searchParams.get('firing') || undefined
  const [activeTab, setActiveTab] = useState(initialTab)
  const [focusInAppId, setFocusInAppId] = useState<number | undefined>(initialFocusID)
  const [focusFiringId, setFocusFiringId] = useState<string | undefined>(initialFocusFiringID)

  const items = [
    { key: 'rules', label: 'Alert Rules', children: <RulesTab canEdit={canEdit} isAdmin={isAdmin} active={activeTab === 'rules'} /> },
    { key: 'channels', label: 'Notification Channels', children: <ChannelsTab canManage={canEdit} isAdmin={isAdmin} currentUserId={user?.id} /> },
    {
      key: 'history', label: 'History',
      children: (
        <HistoryTab
          isAdmin={isAdmin}
          active={activeTab === 'history'}
          focusInAppId={focusInAppId}
          focusFiringId={focusFiringId}
          onFocusConsumed={() => { setFocusInAppId(undefined); setFocusFiringId(undefined) }}
        />
      ),
    },
  ]

  return (
    <>
      <Title level={3} style={{ marginTop: 0 }}>Alerts &amp; Notifications</Title>
      <Card>
        <Tabs items={items} activeKey={activeTab} onChange={setActiveTab} />
      </Card>
    </>
  )
}
