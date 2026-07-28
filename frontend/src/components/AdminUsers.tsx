import { useState } from 'react'
import { Card, Table, Button, Modal, Form, Input, Select, Switch, Space, Tag, Popconfirm, Tooltip, message } from 'antd'
import { useTranslation } from 'react-i18next'
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
  const { t } = useTranslation()
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
    const password = prompt(t('users.resetPasswordPrompt', { username: user.username }))
    if (!password || password.length < 4) {
      if (password !== null) message.error(t('users.passwordMinLength'))
      return
    }
    try {
      await resetPassword(user.id, password)
      message.success(t('users.passwordReset'))
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('users.resetPasswordFailed')))
    }
  }

  const handleUnlock = async (id: number) => {
    try {
      await unlockUser(id)
      message.success(t('users.userUnlocked'))
      crud.refresh()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('users.unlockFailed')))
    }
  }

  const userColumns = [
    {
      title: t('login.username'),
      dataIndex: 'username',
      key: 'username',
    },
    {
      title: t('common.email'),
      dataIndex: 'email',
      key: 'email',
      render: (email: string) => email || '-',
    },
    {
      title: t('users.type'),
      dataIndex: 'auth_type',
      key: 'auth_type',
      render: (authType: string) => <Tag color={authType === 'ldap' ? 'orange' : 'default'}>{authType === 'ldap' ? t('users.ldap') : t('users.local')}</Tag>,
    },
    {
      title: t('users.role'),
      dataIndex: 'role',
      key: 'role',
      render: (role: string) => {
        const colors: Record<string, string> = { admin: 'red', editor: 'blue', viewer: 'green' }
        return <Tag color={colors[role] || 'default'}>{t(`roles.${role}`, role)}</Tag>
      },
    },
    {
      title: t('users.active'),
      dataIndex: 'is_active',
      key: 'is_active',
      render: (active: boolean) => <Tag color={active ? 'green' : 'red'}>{active ? t('users.active') : t('common.disabled')}</Tag>,
    },
    {
      title: t('common.status'),
      dataIndex: 'locked_until',
      key: 'locked_until',
      render: (_v: string | null, record: User) => {
        const remaining = record.locked_until ? new Date(record.locked_until).getTime() - Date.now() : 0
        if (remaining > 0) {
          const minutes = Math.floor(remaining / 60000)
          const seconds = Math.floor((remaining % 60000) / 1000)
          return (
            <Tooltip title={t('users.lockedTooltip', { count: record.failed_login_attempts, minutes, seconds })}>
              <Tag color="red">{t('users.locked', { minutes, seconds })}</Tag>
            </Tooltip>
          )
        }
        if (record.failed_login_attempts > 0) {
          return <Tag color="orange">{t('users.failCount', { count: record.failed_login_attempts })}</Tag>
        }
        return <Tag color="green">{t('common.ok')}</Tag>
      },
    },
    {
      title: t('users.lastLogin'),
      dataIndex: 'last_login_at',
      key: 'last_login_at',
      render: (date: string | null) => date ? new Date(date).toLocaleString() : '-',
    },
    {
      title: t('users.created'),
      dataIndex: 'created_at',
      key: 'created_at',
      render: (date: string) => new Date(date).toLocaleString(),
    },
    {
      title: t('common.actions'),
      key: 'actions',
      render: (_v: unknown, record: User) => (
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {record.locked_until && new Date(record.locked_until).getTime() > Date.now() && (
            <Popconfirm title={t('users.unlockConfirm')} okText={t('common.yes')} cancelText={t('common.no')} onConfirm={() => handleUnlock(record.id)}>
              <Button size="small" type="primary" icon={<ReloadOutlined />} />
            </Popconfirm>
          )}
          <Button size="small" onClick={() => {
            editForm.setFieldsValue({ role: record.role, is_active: record.is_active })
            crud.openEdit(record)
          }} icon={<EditOutlined />} />
          {record.auth_type !== 'ldap' && <Button size="small" onClick={() => handleResetPassword(record)} icon={<KeyOutlined />} />}
          <Popconfirm
            title={t('users.deleteConfirm')}
            okText={t('common.yes')}
            cancelText={t('common.no')}
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
      title={t('users.management')}
      extra={
        <Space>
          {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>{t('common.reset')}</Button>}
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => {
              form.setFieldsValue({ auth_type: 'local' })
              setAuthType('local')
              crud.openCreate()
            }}
          >
            {t('users.addUser')}
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
        title={t('users.createUser')}
        open={crud.modalOpen && !crud.editing}
        onCancel={crud.closeModal}
        onOk={async () => {
          const values = await form.validateFields()
          if (settings['ldap_auto_provision'] === 'true') {
            values.auth_type = 'local'
          }
          await crud.handleCreate(values)
        }}
        okText={t('users.createUser')}
        cancelText={t('common.cancel')}
        width={{ sm: '90%', md: 500 }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="username" label={t('login.username')} rules={[{ required: true, message: t('relay.required') }]}>
            <Input />
          </Form.Item>
          <Form.Item name="email" label={t('common.email')} rules={[{ required: true, message: t('relay.required') }, { type: 'email' }]}>
            <Input />
          </Form.Item>
          <Form.Item name="auth_type" label={t('users.authType')} rules={[{ required: true }]} initialValue="local" hidden={settings['ldap_auto_provision'] === 'true'}>
            <Select onChange={(v) => { setAuthType(v); if (v === 'ldap') form.setFieldsValue({ password: undefined }) }}>
              <Option value="local">{t('users.local')}</Option>
              <Option value="ldap">{t('users.ldap')}</Option>
            </Select>
          </Form.Item>
          <Form.Item
            name="password"
            label={t('common.password')}
            dependencies={['auth_type']}
            hidden={authType === 'ldap'}
            rules={[{ required: authType === 'local' || settings['ldap_auto_provision'] === 'true', min: 8, message: t('setup.min8Chars') }]}
          >
            <Input.Password />
          </Form.Item>
          <Form.Item name="role" label={t('users.role')} rules={[{ required: true }]}>
            <Select>
              <Option value="viewer">{t('roles.viewer')}</Option>
              <Option value="editor">{t('roles.editor')}</Option>
              <Option value="admin">{t('roles.admin')}</Option>
            </Select>
          </Form.Item>
        </Form>
      </Modal>
      <Modal
        title={t('users.editUser')}
        open={crud.modalOpen && !!crud.editing}
        onCancel={crud.closeModal}
        onOk={async () => {
          const values = await editForm.validateFields()
          await crud.handleUpdate(values)
        }}
        okText={t('common.save')}
        cancelText={t('common.cancel')}
        width={{ sm: '90%', md: 500 }}
      >
        <Form form={editForm} layout="vertical">
          <Form.Item label={t('login.username')}>
            <Input value={crud.editing?.username} disabled />
          </Form.Item>
          <Form.Item name="role" label={t('users.role')} rules={[{ required: true }]}>
            <Select>
              <Option value="viewer">{t('roles.viewer')}</Option>
              <Option value="editor">{t('roles.editor')}</Option>
              <Option value="admin">{t('roles.admin')}</Option>
            </Select>
          </Form.Item>
          <Form.Item name="is_active" label={t('users.active')} valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}
