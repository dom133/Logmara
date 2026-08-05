import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
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
import { getRuleTypeLabels, getChannelTypeLabels, historyStatusColor, adminOnlyRuleTypes } from '../constants/alertConstants'
import { HistoryGroup, groupHistoryEntries } from '../components/historyTypes'

const { Title, Text } = Typography

function RulesTab({ canEdit, isAdmin, active }: { canEdit: boolean; isAdmin: boolean; active: boolean }) {
  const { t } = useTranslation()
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
      message.error(t('alerts.failedToLoad'))
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
        message.success(t('alerts.ruleUpdated'))
      } else {
        await createAlert(values)
        message.success(t('alerts.ruleCreated'))
      }
      setModalOpen(false)
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('alerts.saveFailed')))
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteAlert(id)
      message.success(t('alerts.ruleDeleted'))
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('alerts.deleteFailed')))
    }
  }

  const channelName = (id: number) => channels.find(c => c.id === id)?.name || `#${id}`

  const columns = [
    { title: t('common.name'), dataIndex: 'name', key: 'name', render: (v: string, r: Alert) => <Space>{v}{!r.is_active && <Tag>{t('common.disabled')}</Tag>}</Space> },
    { title: t('common.type'), dataIndex: 'rule_type', key: 'rule_type', render: (v: AlertRuleType) => <Tag color="blue">{getRuleTypeLabels(t)[v]}</Tag> },
    {
      title: t('alerts.condition'), key: 'condition', ellipsis: true,
      render: (_v: unknown, r: Alert) => {
        const scope = [
          (r.device_ips || []).length > 0 ? `${r.device_ips.length} ${t('alerts.devices')}` : t('alerts.allDevices'),
          (r.parser_names || []).length > 0 ? `${r.parser_names.length} ${t('alerts.parsers')}` : null,
          (r.field_conditions || []).length > 0 ? `${r.field_conditions.length} ${t('alerts.fieldConditions')}` : null,
        ].filter(Boolean).join(', ')
        if (r.rule_type === 'log_threshold') {
          return `${r.threshold}+ ${t('alerts.matches')} / ${r.window_minutes}m ${t('alerts.on')} ${scope}${r.severity ? `, ${t('alerts.severity')} >= ${r.severity}` : ''}`
        }
        if (r.rule_type === 'device_silence') {
          return `${t('alerts.silentFor')} ${r.threshold}m ${t('alerts.on')} ${scope}`
        }
        if (r.rule_type === 'relay_cert_expiring') {
          return `${t('alerts.warn')} ${r.threshold || 30} ${t('alerts.days')} ${t('alerts.beforeCertExpires')}`
        }
        if (r.rule_type === 'malformed_json') {
          return r.fire_on_every_match ? t('alerts.everyMalformedJson') : t('alerts.anyMalformedJson')
        }
        return r.audit_action_filter ? `${t('alerts.action')} = ${r.audit_action_filter}` : t('alerts.anyAuditAction')
      },
    },
    {
      title: t('alerts.channels'), key: 'channels',
      render: (_v: unknown, r: Alert) => (r.channel_ids || []).map(id => <Tag key={id}>{channelName(id)}</Tag>),
    },
    { title: t('alerts.lastFired'), dataIndex: 'last_fired_at', key: 'last_fired_at', render: (v?: string) => v ? new Date(v).toLocaleString() : '-' },
    {
      title: t('common.actions'), key: 'actions',
      render: (_v: unknown, r: Alert) => {
        const canManage = canEdit && (isAdmin || !adminOnlyRuleTypes.includes(r.rule_type))
        return (
          <Space>
            {canManage && <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />}
            {canManage && <Popconfirm title={t('alerts.deleteConfirm')} onConfirm={() => handleDelete(r.id)}>
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
        {canEdit && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>{t('alerts.newRule')}</Button>}
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
  const { t } = useTranslation()
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
      message.error(t('alerts.channelsFailedToLoad'))
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
        message.success(t('alerts.channelUpdated'))
      } else {
        await createNotificationChannel(req)
        message.success(t('alerts.channelCreated'))
      }
      setModalOpen(false)
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('alerts.channelSaveFailed')))
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteNotificationChannel(id)
      message.success(t('alerts.channelDeleted'))
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('alerts.channelDeleteFailed')))
    }
  }

  const handleTest = async (id: number) => {
    setTestingId(id)
    try {
      await testNotificationChannel(id)
      message.success(t('alerts.testSent'))
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('alerts.testFailed')))
    } finally {
      setTestingId(null)
    }
  }

  const columns = [
    { title: t('common.name'), dataIndex: 'name', key: 'name' },
    { title: t('common.type'), dataIndex: 'type', key: 'type', render: (v: NotificationChannelType) => <Tag color="blue">{getChannelTypeLabels(t)[v]}</Tag> },
    { title: t('common.status'), dataIndex: 'enabled', key: 'enabled', render: (v: boolean) => <Tag color={v ? 'green' : 'red'}>{v ? t('common.enabled') : t('common.disabled')}</Tag> },
    {
      title: t('alerts.owner'), dataIndex: 'created_by_username', key: 'created_by_username',
      render: (username: string | undefined) => {
        if (username) return username
        return <Tag color="default">{t('alerts.noOwner')}</Tag>
      },
    },
    {
      title: t('common.actions'), key: 'actions',
      render: (_v: unknown, r: NotificationChannel) => {
        const canEditRow = r.created_by != null ? canManage && r.created_by === currentUserId : isAdmin
        return (
          <Space>
            <Button size="small" icon={<ExperimentOutlined />} loading={testingId === r.id} onClick={() => handleTest(r.id)}>{t('alerts.test')}</Button>
            {canEditRow && <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)} />}
            {canEditRow && <Popconfirm title={t('alerts.deleteChannelConfirm')} onConfirm={() => handleDelete(r.id)}>
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
        {canManage && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>{t('alerts.newChannel')}</Button>}
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
  const { t } = useTranslation()
  const [entries, setEntries] = useState<NotificationLogEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [clearing, setClearing] = useState(false)
  const [viewing, setViewing] = useState<HistoryGroup | null>(null)

  const loadData = () => {
    setLoading(true)
    getNotificationHistory().then(setEntries).catch(() => message.error(t('alerts.historyFailedToLoad'))).finally(() => setLoading(false))
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
      message.info(t('alerts.noMatchingHistory'))
    }
    onFocusConsumed()
  }, [focusInAppId, focusFiringId, loading, entries, onFocusConsumed])

  const handleClear = async () => {
    setClearing(true)
    try {
      await clearNotificationHistory()
      message.success(t('alerts.historyCleared'))
      loadData()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('alerts.historyClearFailed')))
    } finally {
      setClearing(false)
    }
  }

  const columns = [
    { title: t('common.time'), dataIndex: 'createdAt', key: 'createdAt', render: (v: string) => new Date(v).toLocaleString() },
    { title: t('alerts.alert'), dataIndex: 'alertName', key: 'alertName' },
    {
      title: t('alerts.channels'), key: 'channels',
      render: (_v: unknown, g: HistoryGroup) => (
        <Space size={4} wrap>
          {g.channels.map((c) => (
            <Tag key={c.id} color={historyStatusColor[c.status] || 'default'}>{c.channel_name}</Tag>
          ))}
        </Space>
      ),
    },
    {
      title: t('common.actions'), key: 'actions',
      render: (_v: unknown, g: HistoryGroup) => (
        <Button size="small" icon={<EyeOutlined />} onClick={() => setViewing(g)}>{t('common.details')}</Button>
      ),
    },
  ]

  return (
    <>
      {isAdmin && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
          <Popconfirm title={t('alerts.clearHistoryConfirm')} onConfirm={handleClear}>
            <Button danger loading={clearing} disabled={entries.length === 0}>{t('alerts.clearHistory')}</Button>
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
  const { t } = useTranslation()
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
    { key: 'rules', label: t('alerts.rules'), children: <RulesTab canEdit={canEdit} isAdmin={isAdmin} active={activeTab === 'rules'} /> },
    { key: 'channels', label: t('alerts.notificationChannels'), children: <ChannelsTab canManage={canEdit} isAdmin={isAdmin} currentUserId={user?.id} /> },
    {
      key: 'history', label: t('common.history'),
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
      <Title level={3} style={{ marginTop: 0 }}>{t('alerts.title')}</Title>
      <Card>
        <Tabs items={items} activeKey={activeTab} onChange={setActiveTab} />
      </Card>
    </>
  )
}
