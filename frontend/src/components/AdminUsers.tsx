import { useState } from 'react'
import { Card, Table, Button, Modal, Form, Input, Select, Switch, Space, Tag, Popconfirm, Tooltip, message } from 'antd'
import { PlusOutlined, DeleteOutlined, EditOutlined, KeyOutlined, ReloadOutlined, RestOutlined } from '@ant-design/icons'
import { User, getUsers, createUser, updateUser, deleteUser, resetPassword, unlockUser } from '../services/api'
import { useCrud } from '../hooks/useCRUD'
import { useColumnWidths } from '../hooks/useColumnWidths'
import { getErrorMessage } from '../utils/error'

const { Option } = Select

export interface AdminUsersProps {
  settings: Record<string, string>
}

export default function AdminUsers({ settings }: AdminUsersProps) {
  const [form] = Form.useForm()
  const [editForm] = Form.useForm()
  const [authType, setAuthType] = useState('local')
  const [, setLockoutTick] = useState(0)

  const { enhanceColumns, hasChanges, reset } = useColumnWidths(
    'col_widths_admin',
    [
      { key: 'username', width: 150 },
      { key: 'email', width: 250 },
      { key: 'role', width: 100 },
      { key: 'is_active', width: 100 },
      { key: 'last_login_at', width: 200 },
      { key: 'created_at', width: 200 },
      { key: 'actions', width: 200 },
    ],
  )

  const crud = useCrud<User, Partial<User>, Partial<User>>({
    loadData: getUsers,
    createItem: async (data) => {
      if (settings['ldap_auto_provision'] === 'true') {
        data.auth_type = 'local'
      }
      return createUser(data as { username: string; email: string; password: string; role: string; auth_type: string })
    },
    updateItem: (id, data) => updateUser(id, data as { role?: string; is_active?: boolean }),
    deleteItem: deleteUser,
    entityName: 'User',
    form: form,
  })

  // Live countdown for locked users
  if (crud.items.some(u => u.locked_until && new Date(u.locked_until).getTime() > Date.now())) {
    const _id = setInterval(() => setLockoutTick(t => t + 1), 1000)
    void _id
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
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to reset password'))
    }
  }

  const handleUnlock = async (id: number) => {
    try {
      await unlockUser(id)
      message.success('User unlocked')
      crud.refresh()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to unlock user'))
    }
  }

  const userColumns = [
    {
      title: 'Username',
      dataIndex: 'username',
      key: 'username',
    },
    {
      title: 'Email',
      dataIndex: 'email',
      key: 'email',
      render: (email: string) => email || '-',
    },
    {
      title: 'Type',
      dataIndex: 'auth_type',
      key: 'auth_type',
      render: (t: string) => <Tag color={t === 'ldap' ? 'orange' : 'default'}>{t === 'ldap' ? 'LDAP' : 'Local'}</Tag>,
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
      title: 'Status',
      dataIndex: 'locked_until',
      key: 'locked_until',
      render: (_v: string | null, record: User) => {
        const remaining = record.locked_until ? new Date(record.locked_until).getTime() - Date.now() : 0
        if (remaining > 0) {
          const minutes = Math.floor(remaining / 60000)
          const seconds = Math.floor((remaining % 60000) / 1000)
          return (
            <Tooltip title={`Locked. Fails: ${record.failed_login_attempts}. Unlocks in ${minutes}m ${seconds}s.`}>
              <Tag color="red">Locked ({minutes}m {seconds}s)</Tag>
            </Tooltip>
          )
        }
        if (record.failed_login_attempts > 0) {
          return <Tag color="orange">{record.failed_login_attempts} fail(s)</Tag>
        }
        return <Tag color="green">OK</Tag>
      },
    },
    {
      title: 'Last Login',
      dataIndex: 'last_login_at',
      key: 'last_login_at',
      render: (date: string | null) => date ? new Date(date).toLocaleString() : '-',
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
      render: (_v: unknown, record: User) => (
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {record.locked_until && new Date(record.locked_until).getTime() > Date.now() && (
            <Popconfirm title="Unlock this user?" okText="Yes" cancelText="No" onConfirm={() => handleUnlock(record.id)}>
              <Button size="small" type="primary" icon={<ReloadOutlined />} />
            </Popconfirm>
          )}
          <Button size="small" onClick={() => {
            editForm.setFieldsValue({ role: record.role, is_active: record.is_active })
            crud.openEdit(record)
          }} icon={<EditOutlined />} />
          {record.auth_type !== 'ldap' && <Button size="small" onClick={() => handleResetPassword(record)} icon={<KeyOutlined />} />}
          <Popconfirm
            title="Delete user?"
            okText="Yes"
            cancelText="No"
            onConfirm={() => crud.handleDelete(record.id)}
          >
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </div>
      ),
    },
  ]

  return (
    <Card
      title="User Management"
      extra={
        <Space>
          {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>Reset</Button>}
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => {
              form.setFieldsValue({ auth_type: 'local' })
              setAuthType('local')
              crud.openCreate()
            }}
          >
            Add User
          </Button>
        </Space>
      }
    >
      <Table
        rowKey="id"
        columns={enhanceColumns(userColumns)}
        dataSource={crud.items}
        loading={crud.loading}
        tableLayout="fixed"
        scroll={{ x: 'max-content' }}
      />
      <Modal
        title="Create User"
        open={crud.modalOpen && !crud.editing}
        onCancel={crud.closeModal}
        onOk={async () => {
          const values = await form.validateFields()
          if (settings['ldap_auto_provision'] === 'true') {
            values.auth_type = 'local'
          }
          await crud.handleCreate(values)
        }}
        okText="Create"
        cancelText="Cancel"
        width={{ sm: '90%', md: 500 }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="username" label="Username" rules={[{ required: true, message: 'Required' }]}>
            <Input />
          </Form.Item>
          <Form.Item name="email" label="Email" rules={[{ required: true, message: 'Required' }, { type: 'email' }]}>
            <Input />
          </Form.Item>
          <Form.Item name="auth_type" label="Auth Type" rules={[{ required: true }]} initialValue="local" hidden={settings['ldap_auto_provision'] === 'true'}>
            <Select onChange={(v) => { setAuthType(v); if (v === 'ldap') form.setFieldsValue({ password: undefined }) }}>
              <Option value="local">Local</Option>
              <Option value="ldap">LDAP</Option>
            </Select>
          </Form.Item>
          <Form.Item
            name="password"
            label="Password"
            dependencies={['auth_type']}
            hidden={authType === 'ldap'}
            rules={[{ required: authType === 'local' || settings['ldap_auto_provision'] === 'true', min: 8, message: 'Min 8 characters' }]}
          >
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
        open={crud.modalOpen && !!crud.editing}
        onCancel={crud.closeModal}
        onOk={async () => {
          const values = await editForm.validateFields()
          await crud.handleUpdate(values)
        }}
        okText="Save"
        cancelText="Cancel"
        width={{ sm: '90%', md: 500 }}
      >
        <Form form={editForm} layout="vertical">
          <Form.Item label="Username">
            <Input value={crud.editing?.username} disabled />
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
    </Card>
  )
}
