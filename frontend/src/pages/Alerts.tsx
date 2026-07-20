import { useEffect, useState } from 'react'
import { Card, Table, Button, Tag, Space, Modal, Form, Input, InputNumber, Select, Switch, message, Popconfirm, Tabs, Typography } from 'antd'
import { PlusOutlined, DeleteOutlined, EditOutlined, ExperimentOutlined } from '@ant-design/icons'
import {
  getAlerts, createAlert, updateAlert, deleteAlert, Alert, AlertRequest, AlertRuleType,
  getNotificationChannels, createNotificationChannel, updateNotificationChannel, deleteNotificationChannel, testNotificationChannel,
  NotificationChannel, NotificationChannelRequest, NotificationChannelType,
  getNotificationHistory, NotificationLogEntry,
} from '../services/api'
import { useAuth } from '../services/auth'
import { getErrorMessage } from '../utils/error'

const { Title, Text } = Typography

const ruleTypeLabels: Record<AlertRuleType, string> = {
  log_threshold: 'Log threshold',
  device_silence: 'Device silence',
  config_change: 'Config change',
}

const channelTypeLabels: Record<NotificationChannelType, string> = {
  email: 'Email',
  webhook: 'Webhook',
  slack: 'Slack',
  teams: 'Microsoft Teams',
  in_app: 'In-app',
}

function RulesTab({ canEdit }: { canEdit: boolean }) {
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Alert | null>(null)
  const [form] = Form.useForm()
  const ruleType = Form.useWatch('rule_type', form)

  const loadData = async () => {
    setLoading(true)
    try {
      const [a, c] = await Promise.all([getAlerts(), getNotificationChannels()])
      setAlerts(a)
      setChannels(c)
    } catch {
      message.error('Failed to load alerts')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [])

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ rule_type: 'log_threshold', is_active: true, window_minutes: 5, cooldown_minutes: 15, threshold: 5, channel_ids: [] })
    setModalOpen(true)
  }

  const openEdit = (a: Alert) => {
    setEditing(a)
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
        if (r.rule_type === 'log_threshold') {
          return `${r.threshold}+ matches / ${r.window_minutes}m${r.severity ? ` (severity >= ${r.severity})` : ''}`
        }
        if (r.rule_type === 'device_silence') {
          return `silent for ${r.threshold}m${r.hostname_pattern ? ` (${r.hostname_pattern})` : ''}`
        }
        return r.audit_action_filter ? `action = ${r.audit_action_filter}` : 'any config change'
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

      <Modal
        title={editing ? 'Edit Alert Rule' : 'New Alert Rule'}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
        width={{ sm: '90%', md: 640 }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="description" label="Description">
            <Input.TextArea rows={2} />
          </Form.Item>
          <Form.Item name="rule_type" label="Rule Type" rules={[{ required: true }]}>
            <Select options={Object.entries(ruleTypeLabels).map(([value, label]) => ({ value, label }))} />
          </Form.Item>

          {ruleType === 'log_threshold' && (
            <>
              <Form.Item name="severity" label="Minimum Severity" tooltip="Leave empty to match any severity">
                <Select allowClear options={['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug'].map(s => ({ value: s, label: s }))} />
              </Form.Item>
              <Form.Item name="hostname_pattern" label="Hostname Pattern" tooltip="Substring or glob (*), empty matches all hosts">
                <Input placeholder="router-*" />
              </Form.Item>
              <Form.Item name="app_name_pattern" label="App Name Pattern">
                <Input />
              </Form.Item>
              <Form.Item name="message_pattern" label="Message Pattern">
                <Input placeholder="failed login" />
              </Form.Item>
              <Space.Compact block>
                <Form.Item name="threshold" label="Threshold (matches)" style={{ flex: 1 }} rules={[{ required: true }]}>
                  <InputNumber min={1} style={{ width: '100%' }} />
                </Form.Item>
                <Form.Item name="window_minutes" label="Window (minutes)" style={{ flex: 1 }} rules={[{ required: true }]}>
                  <InputNumber min={1} style={{ width: '100%' }} />
                </Form.Item>
              </Space.Compact>
            </>
          )}

          {ruleType === 'device_silence' && (
            <>
              <Form.Item name="hostname_pattern" label="Hostname Pattern" tooltip="Which devices to watch; empty watches all devices">
                <Input placeholder="router-*" />
              </Form.Item>
              <Form.Item name="threshold" label="Silent After (minutes)" rules={[{ required: true }]}>
                <InputNumber min={1} style={{ width: '100%' }} />
              </Form.Item>
            </>
          )}

          {ruleType === 'config_change' && (
            <Form.Item name="audit_action_filter" label="Action Filter" tooltip="e.g. settings_updated, user_created - leave empty to match any admin action">
              <Input placeholder="settings_updated" />
            </Form.Item>
          )}

          <Form.Item name="cooldown_minutes" label="Cooldown (minutes)" tooltip="Minimum time between repeat notifications for this rule" rules={[{ required: true }]}>
            <InputNumber min={1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="channel_ids" label="Notification Channels">
            <Select mode="multiple" options={channels.map(c => ({ value: c.id, label: `${c.name} (${channelTypeLabels[c.type]})` }))} />
          </Form.Item>
          <Form.Item name="is_active" label="Active" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}

function ChannelsTab({ canEdit }: { canEdit: boolean }) {
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<NotificationChannel | null>(null)
  const [testingId, setTestingId] = useState<number | null>(null)
  const [form] = Form.useForm()
  const channelType = Form.useWatch('type', form)

  const loadData = async () => {
    setLoading(true)
    try {
      setChannels(await getNotificationChannels())
    } catch {
      message.error('Failed to load notification channels')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadData() }, [])

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
    })
    setModalOpen(true)
  }

  const handleSubmit = async () => {
    const values = await form.validateFields()
    const config: Record<string, unknown> = {}
    if (values.type === 'email') config.to = values.to || []
    if (values.type === 'webhook') config.url = values.url
    if (values.type === 'slack' || values.type === 'teams') config.webhook_url = values.webhook_url

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
      title: 'Actions', key: 'actions',
      render: (_v: unknown, r: NotificationChannel) => (
        <Space>
          <Button size="small" icon={<ExperimentOutlined />} loading={testingId === r.id} onClick={() => handleTest(r.id)}>Test</Button>
          {canEdit && <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />}
          {canEdit && <Popconfirm title="Delete channel?" onConfirm={() => handleDelete(r.id)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>}
        </Space>
      ),
    },
  ]

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        {canEdit && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>New Channel</Button>}
      </div>
      <Table dataSource={channels} columns={columns} rowKey="id" loading={loading} size="small" scroll={{ x: 'max-content' }} />

      <Modal
        title={editing ? 'Edit Notification Channel' : 'New Notification Channel'}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={handleSubmit}
        width={{ sm: '90%', md: 560 }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="type" label="Type" rules={[{ required: true }]}>
            <Select options={Object.entries(channelTypeLabels).map(([value, label]) => ({ value, label }))} disabled={!!editing} />
          </Form.Item>

          {channelType === 'email' && (
            <Form.Item name="to" label="Recipients" rules={[{ required: true }]} tooltip="Uses the SMTP relay configured under Admin > Settings">
              <Select mode="tags" open={false} placeholder="you@example.com" tokenSeparators={[',', ' ']} />
            </Form.Item>
          )}
          {channelType === 'webhook' && (
            <>
              <Form.Item name="url" label="Webhook URL" rules={[{ required: true }]}>
                <Input placeholder="https://example.com/hook" />
              </Form.Item>
              <Form.Item name="secret" label="Bearer Token" tooltip="Optional - sent as an Authorization: Bearer header. Leave empty to keep the current one.">
                <Input.Password />
              </Form.Item>
            </>
          )}
          {(channelType === 'slack' || channelType === 'teams') && (
            <Form.Item name="webhook_url" label="Incoming Webhook URL" rules={[{ required: true }]}>
              <Input placeholder="https://hooks.slack.com/services/..." />
            </Form.Item>
          )}
          {channelType === 'in_app' && (
            <Text type="secondary">Delivers to the notification bell for every signed-in user - no further configuration needed.</Text>
          )}

          <Form.Item name="enabled" label="Enabled" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}

function HistoryTab() {
  const [entries, setEntries] = useState<NotificationLogEntry[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    getNotificationHistory().then(setEntries).catch(() => message.error('Failed to load notification history')).finally(() => setLoading(false))
  }, [])

  const columns = [
    { title: 'Time', dataIndex: 'created_at', key: 'created_at', render: (v: string) => new Date(v).toLocaleString() },
    { title: 'Alert', dataIndex: 'alert_name', key: 'alert_name' },
    { title: 'Channel', dataIndex: 'channel_name', key: 'channel_name', render: (v: string, r: NotificationLogEntry) => `${v} (${r.channel_type})` },
    { title: 'Status', dataIndex: 'status', key: 'status', render: (v: string) => <Tag color={v === 'sent' ? 'green' : 'red'}>{v}</Tag> },
    { title: 'Detail', dataIndex: 'detail', key: 'detail', ellipsis: true },
  ]

  return <Table dataSource={entries} columns={columns} rowKey="id" loading={loading} size="small" scroll={{ x: 'max-content' }} />
}

export default function AlertsPage() {
  const { user } = useAuth()
  const canEdit = user?.role === 'admin' || user?.role === 'editor'
  const isAdmin = user?.role === 'admin'

  const items = [
    { key: 'rules', label: 'Alert Rules', children: <RulesTab canEdit={canEdit} /> },
    { key: 'channels', label: 'Notification Channels', children: <ChannelsTab canEdit={isAdmin} /> },
    { key: 'history', label: 'History', children: <HistoryTab /> },
  ]

  return (
    <>
      <Title level={3} style={{ marginTop: 0 }}>Alerts &amp; Notifications</Title>
      <Card>
        <Tabs items={items} />
      </Card>
    </>
  )
}
