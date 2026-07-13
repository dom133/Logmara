import { useState, useEffect } from 'react'
import { Card, Table, Button, Modal, Form, Input, Select, Switch, Space, Tag, message, Tabs, InputNumber, Divider, Popconfirm } from 'antd'
import { PlusOutlined, DeleteOutlined, EditOutlined, KeyOutlined, ThunderboltOutlined, ReloadOutlined, RestOutlined, LoadingOutlined } from '@ant-design/icons'
import { getUsers, createUser, updateUser, deleteUser, resetPassword, getSettings, updateSettings, cleanupLogs, purgeAllLogs, getDeviceStats, testLDAPConnection, User, DeviceStats } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'

const { Option } = Select

export default function Admin() {
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const [editUser, setEditUser] = useState<User | null>(null)
  const [form] = Form.useForm()
  const [editForm] = Form.useForm()

  const [settings, setSettings] = useState<Record<string, string>>({})
  const [settingsForm] = Form.useForm()
  const [devices, setDevices] = useState<DeviceStats[]>([])
  const [ldapEnabled, setLdapEnabled] = useState(false)
  const [testing, setTesting] = useState(false)

  const { enhanceColumns, hasChanges, reset } = useColumnWidths(
    'col_widths_admin',
    [
      { key: 'username', width: 150 },
      { key: 'role', width: 100 },
      { key: 'is_active', width: 100 },
      { key: 'created_at', width: 200 },
      { key: 'actions', width: 200 },
    ],
  )

  const loadUsers = async () => {
    setLoading(true)
    try {
      const data = await getUsers()
      setUsers(data)
    } catch (e: any) {
      message.error('Failed to load users')
    }
    setLoading(false)
  }

  const loadSettings = async () => {
    try {
      const data = await getSettings()
      setSettings(data)
      const formValues: Record<string, any> = { ...data }
      formValues['ldap_enabled'] = data['ldap_enabled'] === 'true'
      formValues['ldap_use_tls'] = data['ldap_use_tls'] === 'true'
      settingsForm.setFieldsValue(formValues)
      setLdapEnabled(data['ldap_enabled'] === 'true')
    } catch (e: any) {
      message.error('Failed to load settings')
    }
  }

  const handleTestLDAP = async () => {
    const values = settingsForm.getFieldsValue([
      'ldap_server', 'ldap_port', 'ldap_use_tls',
      'ldap_base_dn', 'ldap_bind_dn', 'ldap_bind_password',
    ])
    if (!values.ldap_server) {
      message.warning('Server is required')
      return
    }
    setTesting(true)
    try {
      await testLDAPConnection({
        server: values.ldap_server,
        port: values.ldap_port || 389,
        use_tls: values.ldap_use_tls,
        base_dn: values.ldap_base_dn,
        bind_dn: values.ldap_bind_dn,
        bind_password: values.ldap_bind_password,
      })
      message.success('LDAP connection successful')
    } catch (e: any) {
      message.error(e.response?.data?.error || 'LDAP connection failed')
    } finally {
      setTesting(false)
    }
  }

  const loadDevices = async () => {
    try {
      const data = await getDeviceStats()
      setDevices(data)
    } catch (e: any) {
      message.error('Failed to load devices')
    }
  }

  useEffect(() => {
    loadUsers()
    loadSettings()
    loadDevices()
  }, [])

  const handleCreate = async () => {
    const values = await form.validateFields()
    try {
      await createUser(values)
      message.success('User created')
      setModalVisible(false)
      form.resetFields()
      loadUsers()
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to create user')
    }
  }

  const handleEdit = async (user: User) => {
    setEditUser(user)
    editForm.setFieldsValue({ role: user.role, is_active: user.is_active })
  }

  const handleEditSave = async () => {
    if (!editUser) return
    const values = await editForm.validateFields()
    try {
      await updateUser(editUser.id, values)
      message.success('User updated')
      setEditUser(null)
      loadUsers()
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to update user')
    }
  }

  const handleDelete = async (id: number) => {
    try {
      await deleteUser(id)
      message.success('User deleted')
      loadUsers()
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to delete user')
    }
  }

  const handleResetPassword = async (user: User) => {
    const password = prompt(`Enter new password for ${user.username}:`)
    if (!password || password.length < 4) {
      if (password !== null) message.error('Password must be at least 4 characters')
      return
    }
    try {
      await resetPassword(user.id, password)
      message.success('Password reset')
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to reset password')
    }
  }

  const handleSaveSettings = async () => {
    const values = settingsForm.getFieldsValue()
    const strValues: Record<string, string> = {}
    for (const [k, v] of Object.entries(values)) {
      strValues[k] = v === undefined || v === null ? '' : String(v)
    }
    try {
      await updateSettings(strValues)
      message.success('Settings saved')
      loadSettings()
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Failed to save settings')
    }
  }

  const handleCleanup = async () => {
    try {
      const result = await cleanupLogs()
      message.success(`${result.deleted_count} old logs deleted`)
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Cleanup failed')
    }
  }

  const handlePurgeAll = async () => {
    try {
      const result = await purgeAllLogs()
      message.success(`${result.deleted_count} logs purged`)
    } catch (e: any) {
      message.error(e.response?.data?.error || 'Purge failed')
    }
  }

  const userColumns = [
    {
      title: 'Username',
      dataIndex: 'username',
      key: 'username',
    },
    {
      title: 'Role',
      dataIndex: 'role',
      key: 'role',
      render: (role: string) => {
        const colors: Record<string, string> = { admin: 'red', editor: 'blue', viewer: 'green' }
        return <Tag color={colors[role] || 'default'}>{role}</Tag>
      },
    },
    {
      title: 'Active',
      dataIndex: 'is_active',
      key: 'is_active',
      render: (active: boolean) => <Tag color={active ? 'green' : 'red'}>{active ? 'Active' : 'Disabled'}</Tag>,
    },
    {
      title: 'Created',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_: any, record: User) => (
        <Space>
          <Button size="small" onClick={() => handleEdit(record)} icon={<EditOutlined />} />
          <Button size="small" onClick={() => handleResetPassword(record)} icon={<KeyOutlined />} />
          <Popconfirm
            title="Delete user?"
            okText="Yes"
            cancelText="No"
            onConfirm={() => handleDelete(record.id)}
          >
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <div>
      <Card style={{ marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>Admin Panel</h2>
      </Card>

      <Tabs
        defaultActiveKey="users"
        items={[
          {
            key: 'users',
            label: 'Users',
            children: (
              <Card
                title="User Management"
                extra={
                  <Space>
                    {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>Reset</Button>}
                    <Button type="primary" icon={<PlusOutlined />} onClick={() => setModalVisible(true)}>
                      Add User
                    </Button>
                  </Space>
                }
              >
                <Table
                  rowKey="id"
                  columns={enhanceColumns(userColumns)}
                  dataSource={users}
                  loading={loading}
                />
              </Card>
            ),
          },
          {
            key: 'settings',
            label: 'Settings',
            children: (
              <Card title="Application Settings">
                <Form form={settingsForm} layout="vertical" onFinish={handleSaveSettings}>
                  <Form.Item label="Log Retention (days)" name="retention_days">
                    <InputNumber min={1} max={3650} style={{ width: 200 }} />
                  </Form.Item>
                  <Form.Item label="Default Role" name="default_role">
                    <Select style={{ width: 200 }}>
                      <Option value="viewer">Viewer</Option>
                      <Option value="editor">Editor</Option>
                      <Option value="admin">Admin</Option>
                    </Select>
                  </Form.Item>
                  <Form.Item label="JWT Expiry (hours)" name="jwt_expiry">
                    <InputNumber min={1} max={8760} style={{ width: 200 }} />
                  </Form.Item>
                  <Divider />
                  <Space>
                    <Button type="primary" htmlType="submit">
                      Save Settings
                    </Button>
                    <Button danger icon={<ThunderboltOutlined />} onClick={handleCleanup}>
                      Clean Old Logs
                    </Button>
                    <Popconfirm
                      title="Purge ALL logs?"
                      description="This will permanently delete every log entry. This cannot be undone."
                      okText="Yes, purge all"
                      cancelText="Cancel"
                      onConfirm={handlePurgeAll}
                    >
                      <Button danger type="primary">Purge All Logs</Button>
                    </Popconfirm>
                  </Space>
                </Form>
              </Card>
            ),
          },
          {
            key: 'ldap',
            label: 'LDAP',
            children: (
              <Card title="LDAP Authentication">
                <Form form={settingsForm} layout="vertical" onFinish={handleSaveSettings}>
                  <Form.Item label="Enable LDAP" name="ldap_enabled" valuePropName="checked">
                    <Switch checked={ldapEnabled} onChange={(v) => { setLdapEnabled(v); settingsForm.setFieldValue('ldap_enabled', v); }} />
                  </Form.Item>
                  <Form.Item label="Server" name="ldap_server">
                    <Input placeholder="ldap.example.com" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label="Port" name="ldap_port">
                    <InputNumber min={1} max={65535} style={{ width: 200 }} disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label="Use TLS" name="ldap_use_tls" valuePropName="checked">
                    <Switch disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label="Base DN" name="ldap_base_dn">
                    <Input placeholder="dc=example,dc=com" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label="Bind DN" name="ldap_bind_dn">
                    <Input placeholder="cn=admin,dc=example,dc=com" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label="Bind Password" name="ldap_bind_password">
                    <Input.Password disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label="User Filter" name="ldap_user_filter">
                    <Input placeholder="(uid=%s)" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Space>
                    <Button type="primary" htmlType="submit" disabled={!ldapEnabled}>
                      Save LDAP Settings
                    </Button>
                    <Button
                      icon={testing ? <LoadingOutlined /> : undefined}
                      onClick={handleTestLDAP}
                      disabled={!ldapEnabled || testing}
                    >
                      Test Connection
                    </Button>
                  </Space>
                </Form>
              </Card>
            ),
          },
          {
            key: 'devices',
            label: 'Devices',
            children: (
              <Card
                title="Device Statistics"
                extra={
                  <Button icon={<ReloadOutlined />} onClick={loadDevices}>
                    Refresh
                  </Button>
                }
              >
                <Table
                  rowKey="hostname"
                  dataSource={devices}
                  pagination={false}
                  columns={[
                    {
                      title: 'Hostname',
                      dataIndex: 'hostname',
                      key: 'hostname',
                      render: (hostname: string, record: DeviceStats) => {
                        const name = hostname || record.hostname || '-';
                        return (
                          <a onClick={() => window.location.href = `/logs?hostname=${encodeURIComponent(name)}`}>
                            {name}
                          </a>
                        );
                      },
                    },
                    {
                      title: 'Total Logs',
                      dataIndex: 'total_logs',
                      key: 'total_logs',
                      sorter: (a: DeviceStats, b: DeviceStats) => a.total_logs - b.total_logs,
                      render: (v: number) => typeof v === 'number' ? v : 0,
                    },
                    {
                      title: 'Last Seen',
                      dataIndex: 'last_seen',
                      key: 'last_seen',
                      render: (date: string) => date ? new Date(date).toLocaleString() : '-',
                      sorter: (a: DeviceStats, b: DeviceStats) => new Date(a.last_seen).getTime() - new Date(b.last_seen).getTime(),
                    },
                    {
                      title: 'Severity',
                      dataIndex: 'severity_count',
                      key: 'severity_count',
                      render: (sc: Record<string, number>) => {
                        if (!sc || typeof sc !== 'object') return '-';
                        const entries = Object.entries(sc);
                        if (entries.length === 0) return '-';
                        return (
                          <Space wrap>
                            {entries.map(([severity, count]) => (
                              <Tag key={severity} color={severity}>
                                {severity}: {count}
                              </Tag>
                            ))}
                          </Space>
                        );
                      },
                    },
                    {
                      title: 'Matched Parsers',
                      dataIndex: 'matched_parsers',
                      key: 'matched_parsers',
                      render: (parsers: string[]) => {
                        if (!Array.isArray(parsers) || parsers.length === 0) return <span>-</span>;
                        return (
                          <Space wrap>
                            {parsers.map((p) => (
                              <Tag key={p} color="blue">{p}</Tag>
                            ))}
                          </Space>
                        );
                      },
                    },
                    {
                      title: 'Parsed',
                      dataIndex: 'has_parsed',
                      key: 'has_parsed',
                      render: (parsed: boolean) => (
                        <Tag color={parsed ? 'green' : 'orange'}>
                          {parsed ? 'Yes' : 'No'}
                        </Tag>
                      ),
                    },
                  ]}
                />
              </Card>
            ),
          },
        ]}
      />

      <Modal
        title="Create User"
        open={modalVisible}
        onCancel={() => { setModalVisible(false); form.resetFields() }}
        onOk={handleCreate}
        okText="Create"
        cancelText="Cancel"
      >
        <Form form={form} layout="vertical">
          <Form.Item name="username" label="Username" rules={[{ required: true, message: 'Required' }]}>
            <Input />
          </Form.Item>
          <Form.Item name="password" label="Password" rules={[{ required: true, min: 4, message: 'Min 4 characters' }]}>
            <Input.Password />
          </Form.Item>
          <Form.Item name="role" label="Role" rules={[{ required: true }]}>
            <Select>
              <Option value="viewer">Viewer</Option>
              <Option value="editor">Editor</Option>
              <Option value="admin">Admin</Option>
            </Select>
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="Edit User"
        open={!!editUser}
        onCancel={() => setEditUser(null)}
        onOk={handleEditSave}
        okText="Save"
        cancelText="Cancel"
      >
        <Form form={editForm} layout="vertical">
          <Form.Item label="Username">
            <Input value={editUser?.username} disabled />
          </Form.Item>
          <Form.Item name="role" label="Role" rules={[{ required: true }]}>
            <Select>
              <Option value="viewer">Viewer</Option>
              <Option value="editor">Editor</Option>
              <Option value="admin">Admin</Option>
            </Select>
          </Form.Item>
          <Form.Item name="is_active" label="Active" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}