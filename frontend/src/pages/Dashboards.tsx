import { useEffect, useRef, useState } from 'react'
import { Card, Table, Button, Tag, Space, Modal, Form, Input, Select, message, Popconfirm, Typography, List } from 'antd'
import { PlusOutlined, EyeOutlined, EditOutlined, DeleteOutlined, PushpinOutlined, PushpinFilled, RestOutlined, GlobalOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { getDashboards, createDashboard, updateDashboard, deleteDashboard, togglePinDashboard, togglePublicDashboard, Dashboard, DashboardConfig, ParsedField } from '../services/api'
import { getDevices, getParsedFields } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'
import { useAuth } from '../services/auth'

const { Title } = Typography

export default function DashboardsPage() {
  const { user } = useAuth()
  const canEdit = user?.role === 'admin' || user?.role === 'editor'
  const isAdmin = user?.role === 'admin'
  const [dashboards, setDashboards] = useState<Dashboard[]>([])
  const [loading, setLoading] = useState(true)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Dashboard | null>(null)
  const [form] = Form.useForm()
  const [devices, setDevices] = useState<string[]>([])
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

  const loadFieldsForDevices = async (devices: string[]) => {
    const pf = await getParsedFields(devices.length > 0 ? devices : undefined)
    setParsedFields(pf)
  }

  const loadData = async () => {
    setLoading(true)
    try {
      const [d, dv] = await Promise.all([getDashboards(), getDevices()])
      setDashboards(d)
      setDevices(dv)
      await loadAllFields()
    } finally {
      setLoading(false)
    }
  }

  const fieldOptions = Array.from(new Map(parsedFields.map(f => [f.field_name, f])).values()).map(f => ({
    label: f.field_label || f.field_name,
    value: f.field_name,
  }))

  useEffect(() => {
    loadData()
  }, [])

  const handleCreate = async (values: any) => {
    try {
      await createDashboard(values)
      message.success('Dashboard created')
      setModalOpen(false)
      form.resetFields()
      loadData()
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to create dashboard')
    }
  }

  const handleUpdate = async (values: any) => {
    if (!editing) return
    try {
      await updateDashboard(editing.id, values)
      message.success('Dashboard updated')
      setModalOpen(false)
      setEditing(null)
      form.resetFields()
      loadData()
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to update dashboard')
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteDashboard(id)
      message.success('Dashboard deleted')
      loadData()
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to delete dashboard')
    }
  }

  const handleTogglePin = async (id: number) => {
    try {
      const res = await togglePinDashboard(id)
      message.success(res.pinned ? 'Dashboard pinned' : 'Dashboard unpinned')
      loadData()
      window.dispatchEvent(new CustomEvent('dashboards-pinned-changed'))
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to toggle pin')
    }
  }

  const handleTogglePublic = async (id: number) => {
    try {
      const res = await togglePublicDashboard(id)
      message.success(res.is_public ? 'Dashboard is now public' : 'Dashboard is now private')
      loadData()
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to toggle visibility')
    }
  }

  const openCreate = () => {
    setEditing(null)
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
    setModalOpen(true)
  }

  const openEdit = (d: Dashboard) => {
    setEditing(d)
    form.setFieldsValue({
      name: d.name,
      description: d.description,
      config: d.config,
    })
    const devs = d.config?.devices || []
    prevDevices.current = devs
    loadFieldsForDevices(devs)
    setModalOpen(true)
  }

  const columns = [
    {
      title: 'Name',
      dataIndex: 'name',
      key: 'name',
      render: (v: string, r: Dashboard) => (
        <Space>
          <Tag color="cyan">{v}</Tag>
          {r.pinned && <Tag color="gold">📌 Pinned</Tag>}
          {r.is_public && <Tag color="green">Public</Tag>}
          {r.description && <Tag>{r.description}</Tag>}
        </Space>
      ),
    },
    {
      title: 'Devices',
      key: 'devices',
      render: (_: any, r: Dashboard) => {
        const devs = r.config?.devices || []
        return devs.length
          ? devs.slice(0, 3).map(d => <Tag key={d}>{d}</Tag>)
          : <Tag color="default">All</Tag>
      },
    },
    {
      title: 'Fields',
      key: 'fields',
      render: (_: any, r: Dashboard) => {
        const fs = r.config?.fields || []
        return fs.length ? fs.map(f => <Tag key={f} color="green">{f}</Tag>) : <Tag color="default">Default</Tag>
      },
    },
    {
      title: 'Owner',
      dataIndex: 'owner_username',
      key: 'owner_username',
      render: (v: string) => v || '-',
    },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (v: string) => new Date(v).toLocaleDateString(),
    },
    {
      title: 'Last Modified',
      key: 'last_modified',
      render: (_: any, r: Dashboard) => {
        if (!r.updated_at) return '-'
        const date = new Date(r.updated_at).toLocaleDateString()
        const by = r.updated_by_username || r.owner_username
        return `${date} by ${by}`
      },
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_: any, r: Dashboard) => {
        const isOwner = r.owner_id === user?.id
        const canManage = isOwner && canEdit || isAdmin
        return (
          <Space>
            <Button size="small" icon={<EyeOutlined />} onClick={() => navigate(`/dashboards/${r.id}`)}>View</Button>
            {canManage && <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)}>Edit</Button>}
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
              {r.is_public ? 'Public' : 'Private'}
            </Button>}
            {canManage && <Popconfirm title="Delete dashboard?" onConfirm={() => handleDelete(r.id)}>
              <Button size="small" danger icon={<DeleteOutlined />} />
            </Popconfirm>}
          </Space>
        )
      },
    },
  ]

  return (
    <>
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 16 }}>
        <Title level={3}>Custom Dashboards</Title>
        <Space>
          {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>Reset Columns</Button>}
          {canEdit && <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>New Dashboard</Button>}
        </Space>
      </Space>

      {dashboards.length === 0 && !loading && (
        <Card style={{ marginBottom: 16 }}>
          <Typography.Paragraph>
            No dashboards yet. Create one to monitor specific devices with custom fields and filters.
          </Typography.Paragraph>
        </Card>
      )}

      {(() => {
        if (dashboards.length === 0) return null
        if (isAdmin) {
          return (
            <div>
              <Title level={5}>All Dashboards</Title>
              <Table
                dataSource={dashboards}
                columns={enhanceColumns(columns)}
                rowKey="id"
                loading={loading}
                size="small"
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
                <Title level={5}>My Dashboards</Title>
                <Table
                  dataSource={myDashboards}
                  columns={enhanceColumns(columns)}
                  rowKey="id"
                  loading={loading}
                  size="small"
                />
              </div>
            )}
            {publicDashboards.length > 0 && (
              <div>
                <Title level={5}>Public Dashboards</Title>
                <Table
                  dataSource={publicDashboards}
                  columns={enhanceColumns(columns)}
                  rowKey="id"
                  loading={loading}
                  size="small"
                />
              </div>
            )}
          </>
        )
      })()}

      <Modal
        title={editing ? 'Edit Dashboard' : 'New Dashboard'}
        open={modalOpen}
        onCancel={() => { setModalOpen(false); setEditing(null) }}
        onOk={() => { form.validateFields().then(values => editing ? handleUpdate(values) : handleCreate(values)) }}
        width={700}
      >
        <Form form={form} layout="vertical" onValuesChange={async (changed, allValues) => {
      const newDevices = allValues.config?.devices || []
      if (JSON.stringify(newDevices) !== JSON.stringify(prevDevices.current)) {
        prevDevices.current = newDevices
        form.setFieldValue(['config', 'fields'], [])
        await loadFieldsForDevices(newDevices)
      }
    }}>
          <Form.Item name="name" label="Name" rules={[{ required: true, message: 'Name is required' }]}>
            <Input placeholder="e.g. Firewall Monitoring" />
          </Form.Item>
          <Form.Item name="description" label="Description">
            <Input.TextArea rows={2} placeholder="Optional description..." />
          </Form.Item>

          <Form.Item label="Monitored Devices">
            <Form.Item name={['config', 'devices']} noStyle>
              <Select
                mode="multiple"
                placeholder="Select devices to monitor (leave empty for all)"
                style={{ width: '100%' }}
                options={devices.map(d => ({ label: d, value: d }))}
              />
            </Form.Item>
          </Form.Item>

          <Form.Item label="Parsed Fields to Show">
            <Form.Item name={['config', 'fields']} noStyle>
              <Select
                mode="multiple"
                placeholder="Select fields from parsed data (leave empty for default)"
                style={{ width: '100%' }}
                options={fieldOptions}
              />
            </Form.Item>
          </Form.Item>

          <Form.Item label="Filters">
            <Space direction="vertical" style={{ width: '100%' }}>
              <Form.Item label="Severity" noStyle>
                <Form.Item name={['config', 'filters', 'severity']} noStyle>
                  <Select
                    placeholder="All severities"
                    style={{ width: '100%' }}
                    options={[
                      { label: 'All', value: '' },
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
              <Form.Item label="Search Term" noStyle>
                <Form.Item name={['config', 'filters', 'search']} noStyle>
                  <Input placeholder="Filter by keyword in message/hostname" />
                </Form.Item>
              </Form.Item>
            </Space>
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}