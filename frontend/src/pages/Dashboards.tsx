import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Card, Table, Button, Tag, Space, Modal, Form, Input, Select, message, Popconfirm, Typography, Spin } from 'antd'
import { PlusOutlined, EyeOutlined, EditOutlined, DeleteOutlined, PushpinOutlined, PushpinFilled, RestOutlined, GlobalOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { getDashboards, createDashboard, updateDashboard, deleteDashboard, togglePinDashboard, togglePublicDashboard, Dashboard, DashboardConfig, ParsedField } from '../services/api'
import { getDevices, getParsedFields, DeviceStats, resolveDeviceDisplayName } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'
import { useCrud } from '../hooks/useCRUD'
import { useAuth } from '../services/auth'
import { tokens } from '../theme/tokens'
import DashboardFieldFilters from '../components/DashboardFieldFilters'
import { getErrorMessage } from '../utils/error'

const { Title } = Typography

export default function DashboardsPage() {
  const { t } = useTranslation()
  const { user } = useAuth()
  const canEdit = user?.role === 'admin' || user?.role === 'editor'
  const isAdmin = user?.role === 'admin'
  const [form] = Form.useForm()
  const [devices, setDevices] = useState<DeviceStats[]>([])
  const [parsedFields, setParsedFields] = useState<ParsedField[]>([])
  const navigate = useNavigate()
  const prevDevices = useRef<string[]>([])

  const { enhanceColumns, hasChanges, reset } = useColumnWidths(
    'col_widths_dashboards',
    [
      { key: 'name', width: 250 },
      { key: 'devices', width: 200 },
      { key: 'fields', width: 200 },
      { key: 'created_at', width: 160 },
      { key: 'last_modified', width: 180 },
      { key: 'actions', width: 250 },
    ],
  )

  const loadAllFields = async () => {
    const pf = await getParsedFields()
    setParsedFields(pf)
  }

  const loadFieldsForDevices = async (devicesList: string[]) => {
    const pf = await getParsedFields(devicesList.length > 0 ? devicesList : undefined)
    setParsedFields(pf)
  }

  const {
    items: dashboards,
    loading,
    modalOpen,
    editing,
    openCreate: crudOpenCreate,
    openEdit: crudOpenEdit,
    closeModal,
    handleCreate,
    handleUpdate,
    handleDelete,
    refresh,
  } = useCrud<Dashboard, { name: string; description?: string; config: DashboardConfig }, Partial<{ name: string; description: string; config: DashboardConfig }>>({
    loadData: async () => {
      const [d, dv] = await Promise.all([getDashboards(), getDevices()])
      setDevices(dv)
      await loadAllFields()
      return d
    },
    createItem: createDashboard,
    updateItem: updateDashboard,
    deleteItem: deleteDashboard,
    entityName: 'Dashboard',
    form,
  })

  const selectedDevices: string[] = Form.useWatch(['config', 'devices'], form) || []
  const selectedParsers: string[] = Form.useWatch(['config', 'parsers'], form) || []

  const parserOptions = (() => {
    if (selectedDevices.length === 0) return []
    const names = new Set<string>()
    devices.forEach(d => {
      if (selectedDevices.includes(d.fromhost_ip)) {
        (d.matched_parsers || []).forEach(p => names.add(p))
      }
    })
    return Array.from(names).sort().map(p => ({ label: p, value: p }))
  })()

  const fieldOptions = Array.from(
    new Map(
      parsedFields
        .filter(f => selectedParsers.length === 0 || selectedParsers.includes(f.parser_name))
        .map(f => [f.field_name, f]),
    ).values(),
  ).map(f => ({
    label: f.field_label || f.field_name,
    value: f.field_name,
  }))

  const handleTogglePin = async (id: number) => {
    try {
      const res = await togglePinDashboard(id)
      message.success(res.pinned ? t('dashboards.pinned') : t('dashboards.unpinned'))
      refresh()
      window.dispatchEvent(new CustomEvent('dashboards-pinned-changed'))
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('dashboards.failedTogglePin')))
    }
  }

  const handleTogglePublic = async (id: number) => {
    try {
      const res = await togglePublicDashboard(id)
      message.success(res.is_public ? t('dashboard.isPublic') : t('dashboard.isPrivate'))
      refresh()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('dashboards.failedTogglePublic')))
    }
  }

  const openCreate = () => {
    form.resetFields()
    form.setFieldsValue({
      config: {
        devices: [],
        fields: [],
        filters: { severity: '', from: '', to: '', search: '' },
      },
    })
    prevDevices.current = []
    loadAllFields()
    crudOpenCreate()
  }

  const openEdit = (d: Dashboard) => {
    form.setFieldsValue({
      name: d.name,
      description: d.description,
      config: d.config,
    })
    const devs = d.config?.devices || []
    prevDevices.current = devs
    loadFieldsForDevices(devs)
    crudOpenEdit(d)
  }

  const columns = [
    {
      title: t('dashboards.name'),
      dataIndex: 'name',
      key: 'name',
      render: (v: string, r: Dashboard) => (
        <Space>
          <Tag color="cyan">{v}</Tag>
          {r.pinned && <Tag color="gold">{t('dashboards.pinnedTag')}</Tag>}
          {r.is_public && <Tag color="green">{t('dashboard.public')}</Tag>}
          {r.description && (
            <Tag style={{ maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={r.description}>
              {r.description}
            </Tag>
          )}
        </Space>
      ),
    },
    {
      title: t('dashboard.devices'),
      key: 'devices',
      render: (_v: unknown, r: Dashboard) => {
        const devs = r.config?.devices || []
        return devs.length
          ? devs.slice(0, 3).map(d => <Tag key={d}>{d}</Tag>)
          : <Tag color="default">{t('common.all')}</Tag>
      },
    },
    {
      title: t('dashboard.fields'),
      key: 'fields',
      render: (_v: unknown, r: Dashboard) => {
        const fs = r.config?.fields || []
        return fs.length ? fs.map(f => <Tag key={f} color="green">{f}</Tag>) : <Tag color="default">{t('common.default')}</Tag>
      },
    },
    {
      title: t('dashboards.owner'),
      dataIndex: 'owner_username',
      key: 'owner_username',
      render: (v: string) => v || '-',
    },
    {
      title: t('dashboards.created'),
      dataIndex: 'created_at',
      key: 'created_at',
      render: (v: string) => new Date(v).toLocaleDateString(),
    },
    {
      title: t('dashboards.lastModified'),
      key: 'last_modified',
      render: (_v: unknown, r: Dashboard) => {
        if (!r.updated_at) return '-'
        const date = new Date(r.updated_at).toLocaleDateString()
        const by = r.updated_by_username || r.owner_username
        return `${date} ${t('common.by')} ${by}`
      },
    },
    {
      title: t('common.actions'),
      key: 'actions',
      render: (_v: unknown, r: Dashboard) => {
        const isOwner = r.owner_id === user?.id
        const canManage = isOwner && canEdit || isAdmin
        return (
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            <Button size="small" icon={<EyeOutlined />} onClick={() => navigate(`/dashboards/${r.id}`)}>{t('common.view')}</Button>
            {canManage && <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)}>{t('common.edit')}</Button>}
            <Button
              size="small"
              icon={r.pinned ? <PushpinFilled /> : <PushpinOutlined />}
              onClick={() => handleTogglePin(r.id)}
              style={{ color: r.pinned ? '#faad14' : undefined }}
            />
            {(isOwner || isAdmin) && <Button
              size="small"
              icon={<GlobalOutlined />}
              onClick={() => handleTogglePublic(r.id)}
              type={r.is_public ? 'primary' : 'default'}
            >
              {r.is_public ? t('dashboard.public') : t('dashboard.private')}
            </Button>}
            {canManage && <Popconfirm title={t('dashboards.deleteConfirm')} onConfirm={() => handleDelete(r.id)}>
              <Button size="small" danger icon={<DeleteOutlined />} />
            </Popconfirm>}
          </div>
        )
      },
    },
  ]

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 8, marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0, whiteSpace: 'nowrap' }}>{t('dashboards.title')}</Title>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>{t('dashboards.resetColumns')}</Button>}
          {canEdit && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>{t('dashboards.newDashboard')}</Button>}
        </div>
      </div>

      {loading && dashboards.length === 0 && (
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Spin size="large" tip={t('dashboards.loading')} />
        </div>
      )}
      {dashboards.length === 0 && !loading && (
        <Card style={{ marginBottom: 16 }}>
          <Typography.Paragraph>
            {t('dashboards.empty')}
          </Typography.Paragraph>
        </Card>
      )}

      {(() => {
        if (dashboards.length === 0) return null
        if (isAdmin) {
          return (
            <div>
              <Title level={5}>{t('dashboards.allDashboards')}</Title>
              <Table
                dataSource={dashboards}
                columns={enhanceColumns(columns)}
                rowKey="id"
                loading={loading}
                size="small"
                tableLayout="fixed"
                scroll={{ x: 'max-content' }}
              />
            </div>
          )
        }
        const myDashboards = dashboards.filter(d => d.owner_id === user?.id)
        const publicDashboards = dashboards.filter(d => d.owner_id !== user?.id)
        if (myDashboards.length === 0 && publicDashboards.length === 0) return null
        return (
          <>
            {myDashboards.length > 0 && (
              <div style={{ marginBottom: 16 }}>
                <Title level={5}>{t('dashboards.myDashboards')}</Title>
                <Table
                  dataSource={myDashboards}
                  columns={enhanceColumns(columns)}
                  rowKey="id"
                  loading={loading}
                  size="small"
                  scroll={{ x: 'max-content' }}
                />
              </div>
            )}
            {publicDashboards.length > 0 && (
              <div>
                <Title level={5}>{t('dashboards.publicDashboards')}</Title>
                <Table
                  dataSource={publicDashboards}
                  columns={enhanceColumns(columns)}
                  rowKey="id"
                  loading={loading}
                  size="small"
                  scroll={{ x: 'max-content' }}
                />
              </div>
            )}
          </>
        )
      })()}

      <Modal
        title={editing ? t('dashboards.editDashboard') : t('dashboards.newDashboard')}
        open={modalOpen}
        onCancel={closeModal}
        onOk={() => { form.validateFields().then(values => editing ? handleUpdate(values) : handleCreate(values)) }}
        width={{ sm: '90%', md: 700 }}
      >
        <Form form={form} layout="vertical" onValuesChange={async (changed, allValues) => {
          const newDevices = allValues.config?.devices || []
          if (JSON.stringify(newDevices) !== JSON.stringify(prevDevices.current)) {
            prevDevices.current = newDevices
            form.setFieldValue(['config', 'parsers'], [])
            form.setFieldValue(['config', 'fields'], [])
            await loadFieldsForDevices(newDevices)
            return
          }
          if (changed.config && Object.prototype.hasOwnProperty.call(changed.config, 'parsers')) {
            form.setFieldValue(['config', 'fields'], [])
          }
        }}>
          <Form.Item name="name" label={t('dashboards.name')} rules={[{ required: true, message: t('dashboards.nameRequired') }, { max: 50, message: t('dashboards.nameMax') }]}>
            <Input placeholder={t('dashboards.namePlaceholder')} />
          </Form.Item>
          <Form.Item name="description" label={t('dashboards.description')}>
            <Input.TextArea rows={2} placeholder={t('dashboards.descriptionPlaceholder')} />
          </Form.Item>

          <Form.Item label={t('dashboards.monitoredDevices')}>
            <Form.Item name={['config', 'devices']} noStyle>
              <Select
                mode="multiple"
                placeholder={t('dashboards.selectDevices')}
                style={{ width: '100%' }}
                options={devices.map(d => ({ label: resolveDeviceDisplayName(d), value: d.fromhost_ip }))}
              />
            </Form.Item>
          </Form.Item>

          {selectedDevices.length > 0 && (
            <Form.Item label={t('dashboards.parsers')}>
              <Form.Item name={['config', 'parsers']} noStyle>
                <Select
                  mode="multiple"
                  allowClear
                  placeholder={t('dashboards.selectParsers')}
                  style={{ width: '100%' }}
                  options={parserOptions}
                  notFoundContent={t('dashboards.noParsersFound')}
                />
              </Form.Item>
            </Form.Item>
          )}

          <Form.Item label={t('dashboards.parsedFieldsToShow')}>
            <Form.Item name={['config', 'fields']} noStyle>
              <Select
                mode="multiple"
                placeholder={t('dashboards.selectFields')}
                style={{ width: '100%' }}
                options={fieldOptions}
              />
            </Form.Item>
          </Form.Item>

          <Form.Item label={t('dashboards.filters')}>
            <Space direction="vertical" style={{ width: '100%' }}>
              <Form.Item label={t('dashboard.severity')} noStyle>
                <Form.Item name={['config', 'filters', 'severity']} noStyle>
                  <Select
                    placeholder={t('common.all')}
                    style={{ width: '100%' }}
                    options={[
                      { label: t('common.all'), value: '' },
                      { label: 'emerg', value: 'emerg' },
                      { label: 'alert', value: 'alert' },
                      { label: 'crit', value: 'crit' },
                      { label: 'err', value: 'err' },
                      { label: 'warning', value: 'warning' },
                      { label: 'notice', value: 'notice' },
                      { label: 'info', value: 'info' },
                      { label: 'debug', value: 'debug' },
                    ]}
                  />
                </Form.Item>
              </Form.Item>
              <Form.Item label={t('dashboards.searchTerm')} noStyle>
                <Form.Item name={['config', 'filters', 'search']} noStyle>
                  <Input placeholder={t('dashboards.searchPlaceholder')} />
                </Form.Item>
              </Form.Item>
            </Space>
          </Form.Item>

          <Form.Item label={t('dashboards.fieldFilters')}>
            <Space direction="vertical" style={{ width: '100%' }}>
              <Form.Item name={['config', 'filters', 'fieldFilters']} noStyle>
                <DashboardFieldFilters availableFields={parsedFields.map(f => f.field_name)} />
              </Form.Item>
            </Space>
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}
