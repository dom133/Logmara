import { useState, useEffect } from 'react'
import { Card, Table, Button, Modal, Form, Input, Select, Switch, Space, Tag, message, Tabs, InputNumber, Divider, Popconfirm, Descriptions, Result, Alert, Tooltip } from 'antd'
import { PlusOutlined, DeleteOutlined, EditOutlined, KeyOutlined, ThunderboltOutlined, ReloadOutlined, RestOutlined, LoadingOutlined, UploadOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { getUsers, createUser, updateUser, deleteUser, resetPassword, getSettings, updateSettings, cleanupLogs, purgeAllLogs, getDeviceStats, testLDAPConnection, updateDeviceAlias, getSlowQueries, clearSlowQueries, uploadSSLCerts, getContainersHealth, User, DeviceStats, SlowQueryRecord, ContainersHealthResponse } from '../services/api'
import { useColumnWidths } from '../hooks/useColumnWidths'
import SeverityTag from '../components/SeverityTag'
import { getErrorMessage } from '../utils/error'
import { useAuth } from '../services/auth'

const { Option } = Select

export default function Admin() {
  const { refreshUser } = useAuth()
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(false)
  const [modalVisible, setModalVisible] = useState(false)
  const [editUser, setEditUser] = useState<User | null>(null)
  const [form] = Form.useForm()
  const [editForm] = Form.useForm()

  const [settings, setSettings] = useState<Record<string, string>>({})
  const [settingsForm] = Form.useForm()
  const [devices, setDevices] = useState<DeviceStats[]>([])
  const [devicesLoading, setDevicesLoading] = useState(false)
  const [editDevice, setEditDevice] = useState<DeviceStats | null>(null)
  const [editDeviceForm] = Form.useForm()
  const [ldapEnabled, setLdapEnabled] = useState(false)
  const [ldapAutoProvision, setLdapAutoProvision] = useState(false)
  const [ldapUseTls, setLdapUseTls] = useState(false)
  const [ldapVerifyCert, setLdapVerifyCert] = useState(true)
  const [httpsEnabled, setHttpsEnabled] = useState(false)
  const [httpsRedirect, setHttpsRedirect] = useState(false)
  const [relayEnabled, setRelayEnabled] = useState(false)
  const [smtpEnabled, setSmtpEnabled] = useState(false)
  const [testing, setTesting] = useState(false)
  const [purgeModalOpen, setPurgeModalOpen] = useState(false)
  const [pauseDuringPurge, setPauseDuringPurge] = useState(false)
  const [purging, setPurging] = useState(false)
  const [authType, setAuthType] = useState('local')
  const [slowQueries, setSlowQueries] = useState<SlowQueryRecord[]>([])
  const [slowQueriesLoading, setSlowQueriesLoading] = useState(false)
  const [sslUploading, setSslUploading] = useState(false)
  const [certFile, setCertFile] = useState<File | null>(null)
  const [keyFile, setKeyFile] = useState<File | null>(null)
  const [certInfo, setCertInfo] = useState<any>(null)
  const [health, setHealth] = useState<ContainersHealthResponse | null>(null)
  const [healthLoading, setHealthLoading] = useState(false)

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

  const loadUsers = async () => {
    setLoading(true)
    try {
      const data = await getUsers()
      setUsers(data)
    } catch {
      message.error('Failed to load users')
    }
    setLoading(false)
  }

  const loadSettings = async () => {
    try {
      const data = await getSettings()
      setSettings(data)
      const formValues: Record<string, unknown> = { ...data }
      formValues['ldap_enabled'] = data['ldap_enabled'] === 'true'
      formValues['ldap_use_tls'] = data['ldap_use_tls'] === 'true'
      formValues['ldap_verify_cert'] = data['ldap_verify_cert'] === 'true'
      formValues['ldap_auto_provision'] = data['ldap_auto_provision'] === 'true'
      if (data['ldap_port']) formValues['ldap_port'] = parseInt(data['ldap_port'], 10)
      if (data['retention_days']) formValues['retention_days'] = parseInt(data['retention_days'], 10)
      if (data['session_timeout_min'] !== undefined && data['session_timeout_min'] !== '') formValues['session_timeout_min'] = parseInt(data['session_timeout_min'], 10)
      formValues['https_enabled'] = data['https_enabled'] === 'true'
      formValues['https_redirect'] = data['https_redirect'] === 'true'
      formValues['notifications_enabled'] = data['notifications_enabled'] === 'true'
      formValues['relay_ingestion_enabled'] = data['relay_ingestion_enabled'] === 'true'
      formValues['smtp_enabled'] = data['smtp_enabled'] === 'true'
      formValues['smtp_use_tls'] = data['smtp_use_tls'] === 'true'
      if (data['smtp_port']) formValues['smtp_port'] = parseInt(data['smtp_port'], 10)
      settingsForm.setFieldsValue(formValues)
      setLdapEnabled(data['ldap_enabled'] === 'true')
      setLdapAutoProvision(data['ldap_auto_provision'] === 'true')
      setLdapUseTls(data['ldap_use_tls'] === 'true')
      setLdapVerifyCert(data['ldap_verify_cert'] !== 'false')
      setHttpsEnabled(data['https_enabled'] === 'true')
      setHttpsRedirect(data['https_redirect'] === 'true')
      setRelayEnabled(data['relay_ingestion_enabled'] === 'true')
      setSmtpEnabled(data['smtp_enabled'] === 'true')
    } catch {
      message.error('Failed to load settings')
    }
  }

  const handleTestLDAP = async () => {
    const values = settingsForm.getFieldsValue([
      'ldap_server', 'ldap_port', 'ldap_use_tls', 'ldap_verify_cert', 'ldap_ca_cert',
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
        port: Number(values.ldap_port) || 389,
        use_tls: getLdapBool('ldap_use_tls'),
        verify_cert: getLdapBool('ldap_verify_cert'),
        ca_cert: values.ldap_ca_cert || '',
        base_dn: values.ldap_base_dn,
        bind_dn: values.ldap_bind_dn,
        bind_password: values.ldap_bind_password,
      })
      message.success('LDAP connection successful')
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'LDAP connection failed'))
    } finally {
      setTesting(false)
    }
  }

  const loadDevices = async () => {
    setDevicesLoading(true)
    try {
      const data = await getDeviceStats()
      setDevices(data)
    } catch {
      message.error('Failed to load devices')
    } finally {
      setDevicesLoading(false)
    }
  }

  const handleEditDeviceSave = async () => {
    if (!editDevice) return
    const values = editDeviceForm.getFieldsValue()
    try {
      await updateDeviceAlias(editDevice.fromhost_ip, values.display_name)
      message.success('Device alias updated')
      setEditDevice(null)
      loadDevices()
    } catch {
      message.error('Failed to update alias')
    }
  }

  const loadSlowQueries = async () => {
    setSlowQueriesLoading(true)
    try {
      const data = await getSlowQueries()
      setSlowQueries(data)
    } catch {
      message.error('Failed to load slow queries')
    } finally {
      setSlowQueriesLoading(false)
    }
  }

  const loadHealth = async () => {
    setHealthLoading(true)
    try {
      const data = await getContainersHealth()
      setHealth(data)
    } catch {
      message.error('Failed to load health status')
    } finally {
      setHealthLoading(false)
    }
  }

  const handleClearSlowQueries = async () => {
    try {
      await clearSlowQueries()
      message.success('Slow query log cleared')
      loadSlowQueries()
    } catch {
      message.error('Failed to clear slow queries')
    }
  }

  const handleUploadSSLCerts = async () => {
    if (!certFile || !keyFile) {
      message.warning('Both certificate and key files are required')
      return
    }
    setSslUploading(true)
    try {
      const result = await uploadSSLCerts(certFile, keyFile)
      message.success(result.message || 'SSL certificates uploaded')
      setCertFile(null)
      setKeyFile(null)
      if (result.cert_info) {
        setCertInfo(result.cert_info)
      }
      loadSettings()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to upload SSL certificates'))
    } finally {
      setSslUploading(false)
    }
  }

  useEffect(() => {
    loadUsers()
    loadSettings()
    loadDevices()
    loadSlowQueries()
    loadHealth()
  }, [])

const handleCreate = async () => {
    const values = await form.validateFields()
    if (settings['ldap_auto_provision'] === 'true') {
      values.auth_type = 'local'
    }
    try {
      await createUser(values)
      message.success('User created')
      setModalVisible(false)
      form.resetFields()
      loadUsers()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to create user'))
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
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to update user'))
    }
  }

const handleDelete = async (id: number) => {
    try {
      await deleteUser(id)
      message.success('User deleted')
      loadUsers()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to delete user'))
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
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Failed to reset password'))
    }
  }

const handleSaveSettings = async () => {
    const values = settingsForm.getFieldsValue()
    const strValues: Record<string, string> = {}
    for (const [k, v] of Object.entries(values)) {
      strValues[k] = v === undefined || v === null ? '' : String(v)
    }
    try {
      const result = await updateSettings(strValues)
      if (result?.nginx_reload_error) {
        message.warning(`Settings saved, but nginx reload failed: ${result.nginx_reload_error}`)
      } else if (result?.relay_reload_error) {
        message.warning(`Settings saved, but relay config reload failed: ${result.relay_reload_error}`)
      } else {
        message.success('Settings saved')
      }
      loadSettings()
      // notifications_enabled/relay_ingestion_enabled control which items
      // show in the sidebar (see App.tsx's navItems) - re-fetch /auth/me so
      // that updates immediately instead of only after the next page load.
      refreshUser()
    } catch (e: unknown) {
      const errMsg = getErrorMessage(e, 'Failed to save settings')
      message.error(errMsg)
    }
  }

  const getLdapBool = (key: string) => {
    const v = settingsForm.getFieldValue(key)
    return v === true || v === 'true' || v === 1
  }

const handleCleanup = async () => {
    try {
      const result = await cleanupLogs()
      message.success(`${result.deleted_count} old logs deleted`)
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Cleanup failed'))
    }
  }

  const handlePurgeAll = async () => {
    setPurging(true)
    try {
      const result = await purgeAllLogs(pauseDuringPurge)
      message.success(result.message || 'All logs purged')
      setPurgeModalOpen(false)
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Purge failed'))
    } finally {
      setPurging(false)
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
          <Button size="small" onClick={() => handleEdit(record)} icon={<EditOutlined />} />
          {record.auth_type !== 'ldap' && <Button size="small" onClick={() => handleResetPassword(record)} icon={<KeyOutlined />} />}
          <Popconfirm
            title="Delete user?"
            okText="Yes"
            cancelText="No"
            onConfirm={() => handleDelete(record.id)}
          >
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </div>
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
                  <div style={{ display: 'flex', gap: 8 }}>
                    {hasChanges && <Button size="small" icon={<RestOutlined />} onClick={reset}>Reset</Button>}
                    <Button type="primary" icon={<PlusOutlined />} onClick={() => {
                    form.setFieldsValue({ auth_type: 'local' })
                    setAuthType('local')
                    setModalVisible(true)
                  }}>
                      Add User
                    </Button>
                  </div>
                }
              >
                <Table
                  rowKey="id"
                  columns={enhanceColumns(userColumns)}
                  dataSource={users}
                  loading={loading}
                  tableLayout="fixed"
                  scroll={{ x: 'max-content' }}
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
                    <InputNumber min={1} max={3650} style={{ width: '100%' }} />
                  </Form.Item>
<Form.Item label="Session Timeout (minutes)" name="session_timeout_min">
                     <InputNumber min={1} max={10080} style={{ width: '100%' }} />
                   </Form.Item>
                   <Divider orientation="left">CORS</Divider>
                   <Form.Item
                     label="Allowed CORS Origins"
                     name="cors_origins"
                     tooltip="Comma-separated list of origins allowed to call the API from a browser (e.g. http://localhost:3000,https://example.com). Leave empty to only allow the origin the app is served from."
                   >
                     <Input placeholder="http://localhost:3000,https://yourdomain.com" />
                   </Form.Item>
                   <Divider orientation="left">HTTPS</Divider>
                   <Form.Item label="Enable HTTPS" name="https_enabled" valuePropName="checked">
                     <Switch checked={httpsEnabled} onChange={(v) => {
                       setHttpsEnabled(v)
                       settingsForm.setFieldValue('https_enabled', v)
                       if (!v) {
                         setCertInfo(null); setCertFile(null); setKeyFile(null)
                         setHttpsRedirect(false)
                         settingsForm.setFieldValue('https_redirect', false)
                       }
                     }} />
                   </Form.Item>
                   <Form.Item label="Redirect HTTP to HTTPS" name="https_redirect" valuePropName="checked">
                     <Switch checked={httpsRedirect} disabled={!httpsEnabled} onChange={(v) => { setHttpsRedirect(v); settingsForm.setFieldValue('https_redirect', v) }} />
                   </Form.Item>
                    <Form.Item label="Upload Certificate (.pem/.crt)">
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                        <input
                          type="file"
                          accept=".pem,.crt,.cer"
                          style={{ display: 'none' }}
                          id="ssl-cert-upload"
                          disabled={!httpsEnabled}
                          onChange={(e) => {
                            const file = e.target.files?.[0]
                            if (file) { setCertFile(file); message.success(`Certificate selected: ${file.name}`) }
                            e.target.value = ''
                          }}
                        />
                        <Button icon={<UploadOutlined />} disabled={!httpsEnabled} onClick={() => { document.getElementById('ssl-cert-upload')?.click() }}>
                          {certFile ? `Selected: ${certFile.name}` : 'Choose Certificate'}
                        </Button>
                      </div>
                    </Form.Item>
                    <Form.Item label="Upload Private Key (.key)">
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                        <input
                          type="file"
                          accept=".key,.pem"
                          style={{ display: 'none' }}
                          id="ssl-key-upload"
                          disabled={!httpsEnabled}
                          onChange={(e) => {
                            const file = e.target.files?.[0]
                            if (file) { setKeyFile(file); message.success(`Key selected: ${file.name}`) }
                            e.target.value = ''
                          }}
                        />
                        <Button icon={<UploadOutlined />} disabled={!httpsEnabled} onClick={() => { document.getElementById('ssl-key-upload')?.click() }}>
                          {keyFile ? `Selected: ${keyFile.name}` : 'Choose Key'}
                        </Button>
                      </div>
                    </Form.Item>
<Form.Item>
                      <Button
                        type="primary"
                        icon={<UploadOutlined />}
                        loading={sslUploading}
                        disabled={!httpsEnabled || !certFile || !keyFile || sslUploading}
                        onClick={handleUploadSSLCerts}
                        block
                      >
                        {sslUploading ? 'Uploading...' : 'Upload Certificates'}
                      </Button>
                    </Form.Item>
                    <Divider orientation="left">Notifications</Divider>
                    <Form.Item label="Enable Notifications" name="notifications_enabled" valuePropName="checked" tooltip="Master switch for alert rule evaluation and delivery through any channel">
                      <Switch />
                    </Form.Item>
                    <Form.Item label="Enable SMTP" name="smtp_enabled" valuePropName="checked" tooltip="Required for email notification channels">
                      <Switch checked={smtpEnabled} onChange={(v) => { setSmtpEnabled(v); settingsForm.setFieldValue('smtp_enabled', v) }} />
                    </Form.Item>
                    <Form.Item label="SMTP Host" name="smtp_host">
                      <Input placeholder="smtp.example.com" disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label="SMTP Port" name="smtp_port">
                      <InputNumber min={1} max={65535} style={{ width: '100%' }} disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label="SMTP Username" name="smtp_username">
                      <Input placeholder="notifications@example.com" disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label="SMTP Password" name="smtp_password">
                      <Input.Password disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label="From Address" name="smtp_from">
                      <Input placeholder="syslytics@example.com" disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label="Use STARTTLS" name="smtp_use_tls" valuePropName="checked">
                      <Switch disabled={!smtpEnabled} />
                    </Form.Item>
                    <Divider orientation="left">Syslog Relay</Divider>
                    <Form.Item
                      label="Enable Syslog Relay Ingestion"
                      name="relay_ingestion_enabled"
                      valuePropName="checked"
                      tooltip="Accept syslog forwarded by remote relays (mTLS + IP whitelist) in other VLANs. Once enabled, whitelist and certificate management appear in a separate 'Syslog Relay' menu (admin-only)."
                    >
                      <Switch checked={relayEnabled} onChange={(v) => { setRelayEnabled(v); settingsForm.setFieldValue('relay_ingestion_enabled', v) }} />
                    </Form.Item>
                    <Form.Item
                      label="Central Server Address"
                      name="relay_central_host"
                      tooltip="This server's hostname/IP as reachable from a relay's VLAN, pre-filled into every relay.conf bundle generated on the Syslog Relay page. Falls back to the RELAY_CENTRAL_HOST env var, then 127.0.0.1, if left empty."
                    >
                      <Input placeholder="e.g. syslog.example.com or 10.0.0.5" disabled={!relayEnabled} />
                    </Form.Item>
                    {certInfo && (
                      <Result
                        status={certInfo.error ? 'error' : 'success'}
                        icon={<SafetyCertificateOutlined />}
                        title={certInfo.error || 'Certificate Verified'}
                        subTitle={certInfo.subject}
                      >
                        <Descriptions bordered column={1} size="small">
                          <Descriptions.Item label="Subject"><span style={{ wordBreak: 'break-all' }}>{certInfo.subject || '-'}</span></Descriptions.Item>
                          <Descriptions.Item label="Issuer"><span style={{ wordBreak: 'break-all' }}>{certInfo.issuer || '-'}</span></Descriptions.Item>
                          <Descriptions.Item label="Valid From">{certInfo.valid_from || '-'}</Descriptions.Item>
                          <Descriptions.Item label="Valid To">{certInfo.valid_to || '-'}</Descriptions.Item>
                          <Descriptions.Item label="DNS Names"><span style={{ wordBreak: 'break-all' }}>{Array.isArray(certInfo.dns_names) && certInfo.dns_names.length > 0 ? certInfo.dns_names.join(', ') : '-'}</span></Descriptions.Item>
                          {certInfo.error && <Descriptions.Item label="Error"><span style={{ wordBreak: 'break-all' }}>{certInfo.error}</span></Descriptions.Item>}
                        </Descriptions>
                      </Result>
                    )}
                  <Divider />
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                    <Button type="primary" htmlType="submit">
                      Save Settings
                    </Button>
                    <Button danger icon={<ThunderboltOutlined />} onClick={handleCleanup}>
                      Clean Old Logs
                    </Button>
                    <Button danger type="primary" onClick={() => setPurgeModalOpen(true)}>
                      Purge All Logs
                    </Button>
                    <Modal
                      title="Purge ALL logs?"
                      open={purgeModalOpen}
                      onOk={handlePurgeAll}
                      onCancel={() => setPurgeModalOpen(false)}
                      okText="Yes, purge all"
                      cancelText="Cancel"
                      okButtonProps={{ danger: true, disabled: purging, loading: purging }}
                    >
                      <p>This will permanently delete every log entry. This cannot be undone.</p>
                      <div style={{ marginTop: 16 }}>
                        <label style={{ cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 8 }}>
                          <input
                            type="checkbox"
                            checked={pauseDuringPurge}
                            onChange={(e) => setPauseDuringPurge(e.target.checked)}
                          />
                          Pause ingestion during purge (resumes after)
                        </label>
                      </div>
                    </Modal>
                  </div>
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
                    <InputNumber min={1} max={65535} style={{ width: '100%' }} disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label="Use TLS" name="ldap_use_tls" valuePropName="checked">
                    <Switch checked={ldapUseTls} disabled={!ldapEnabled} onChange={(v) => { setLdapUseTls(v); settingsForm.setFieldValue('ldap_use_tls', v) }} />
                  </Form.Item>
                  <Divider orientation="left">TLS/Certificate</Divider>
                  <Form.Item label="Verify TLS Certificate" name="ldap_verify_cert" valuePropName="checked">
                    <Switch checked={ldapVerifyCert} disabled={!ldapEnabled} onChange={(v) => { setLdapVerifyCert(v); settingsForm.setFieldValue('ldap_verify_cert', v) }} />
                  </Form.Item>
                  <Form.Item
                    label="Custom CA Certificate (PEM)"
                    name="ldap_ca_cert"
                  >
                    <Input.TextArea rows={4} placeholder="Paste PEM certificate or upload a file..." disabled={!ldapEnabled} style={{ resize: 'none' }} />
                  </Form.Item>
                  <Form.Item>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                      <input
                        type="file"
                        accept=".pem,.crt,.cer"
                        style={{ display: 'none' }}
                        id="ca-cert-upload"
                        disabled={!ldapEnabled}
                        onChange={(e) => {
                          const file = e.target.files?.[0]
                          if (!file) return
                          const reader = new FileReader()
                          reader.onload = (ev) => {
                            const text = ev.target?.result
                            if (typeof text === 'string') {
                              settingsForm.setFieldValue('ldap_ca_cert', text)
                              message.success('PEM file loaded')
                            }
                          }
                          reader.onerror = () => message.error('Failed to read PEM file')
                          reader.readAsText(file)
                          e.target.value = ''
                        }}
                      />
                      <Button icon={<UploadOutlined />} disabled={!ldapEnabled} block onClick={() => { document.getElementById('ca-cert-upload')?.click() }}>
                        Upload PEM
                      </Button>
                    </div>
                  </Form.Item>
                  <Divider orientation="left">Connection</Divider>
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
                  <Divider orientation="left">Attribute Mapping</Divider>
                  <Form.Item label="Username Attribute" name="ldap_username_attr">
                    <Input placeholder="uid" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label="Email Attribute" name="ldap_email_attr">
                    <Input placeholder="mail" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label="Auto-Provision LDAP Users" name="ldap_auto_provision" valuePropName="checked">
                    <Switch disabled={!ldapEnabled} onChange={(v) => { setLdapAutoProvision(v); settingsForm.setFieldValue('ldap_auto_provision', v); }} />
                  </Form.Item>
                  <Form.Item label="Default Role (auto-provisioned)" name="ldap_default_role">
                    <Select style={{ width: '100%' }} disabled={!ldapEnabled || !ldapAutoProvision}>
                      <Option value="viewer">Viewer</Option>
                      <Option value="editor">Editor</Option>
                      <Option value="admin">Admin</Option>
                    </Select>
                  </Form.Item>
                  <div style={{ display: 'flex', gap: 8 }}>
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
                  </div>
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
                  loading={devicesLoading}
                  rowKey="fromhost_ip"
                  dataSource={devices}
                  pagination={false}
                  tableLayout="fixed"
                  scroll={{ x: 'max-content' }}
                  columns={[
                    {
                      title: 'Source IP',
                      dataIndex: 'fromhost_ip',
                      key: 'fromhost_ip',
                      width: 140,
                      render: (ip: string) => ip || '-',
                    },
                    {
                      title: 'Hostname',
                      dataIndex: 'hostname',
                      key: 'hostname',
                      width: 180,
                      ellipsis: true,
                      render: (hostname: string, record: DeviceStats) => {
                        const name = record.display_name || hostname || record.hostname || '-';
                        return (
                          <a onClick={() => window.location.href = `/logs?fromhost_ip=${encodeURIComponent(record.fromhost_ip)}`}>
                            {name}
                          </a>
                        );
                      },
                    },
                    {
                      title: 'Total Logs',
                      dataIndex: 'total_logs',
                      key: 'total_logs',
                      width: 100,
                      sorter: (a: DeviceStats, b: DeviceStats) => a.total_logs - b.total_logs,
                      render: (v: number) => typeof v === 'number' ? v : 0,
                    },
                    {
                      title: 'Last Seen',
                      dataIndex: 'last_seen',
                      key: 'last_seen',
                      width: 170,
                      render: (date: string) => date ? new Date(date).toLocaleString() : '-',
                      sorter: (a: DeviceStats, b: DeviceStats) => new Date(a.last_seen).getTime() - new Date(b.last_seen).getTime(),
                    },
                    {
                      title: 'Severity',
                      dataIndex: 'severity_count',
                      key: 'severity_count',
                      width: 220,
                      render: (sc: Record<string, number>) => {
                        if (!sc || typeof sc !== 'object') return '-';
                        const entries = Object.entries(sc).filter(([, count]) => count > 0);
                        if (entries.length === 0) return '-';
                        return (
                          <Space wrap>
                            {entries.map(([severity, count]) => (
                              <Tag key={severity}>
                                <SeverityTag severity={severity} /> {count}
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
                      width: 200,
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
                      width: 90,
                      render: (parsed: boolean) => (
                        <Tag color={parsed ? 'green' : 'orange'}>
                          {parsed ? 'Yes' : 'No'}
                        </Tag>
                      ),
                    },
                    {
                      title: 'Actions',
                      key: 'actions',
                      width: 130,
                      render: (_v, record: DeviceStats) => (
                        <Button
                          type="link"
                          icon={<EditOutlined />}
                          onClick={() => {
                            setEditDevice(record)
                            editDeviceForm.setFieldsValue({ display_name: record.display_name || record.hostname })
                          }}
                        >
                          Edit Name
                        </Button>
                      ),
                    },
                  ]}
                />
              </Card>
            ),
          },
          {
            key: 'slow_queries',
            label: 'Slow Queries',
            children: (
              <Card
                title="Slow Query Log"
                extra={
                  <div style={{ display: 'flex', gap: 8 }}>
                    <Button icon={<ReloadOutlined />} onClick={loadSlowQueries}>
                      Refresh
                    </Button>
                    <Button danger icon={<DeleteOutlined />} onClick={handleClearSlowQueries}>
                      Clear
                    </Button>
                  </div>
                }
              >
                <Table
                  loading={slowQueriesLoading}
                  rowKey={(record, i) => String(i)}
                  dataSource={slowQueries}
                  pagination={{ pageSize: 50 }}
                  tableLayout="fixed"
                  scroll={{ x: 'max-content' }}
                  columns={[
                    {
                      title: 'Query',
                      dataIndex: 'name',
                      key: 'name',
                      width: 400,
                      render: (name: string) => (
                        <Tag color="orange" style={{ whiteSpace: 'normal', wordBreak: 'break-all', maxWidth: '100%' }}>{name}</Tag>
                      ),
                    },
                    {
                      title: 'Duration',
                      dataIndex: 'duration_ms',
                      key: 'duration_ms',
                      width: 120,
                      sorter: (a: SlowQueryRecord, b: SlowQueryRecord) => a.duration_ms - b.duration_ms,
                      render: (ms: number) => {
                        const color = ms > 5000 ? 'red' : ms > 1000 ? 'orange' : 'green'
                        return <Tag color={color}>{ms} ms</Tag>
                      },
                    },
                    {
                      title: 'Timestamp',
                      dataIndex: 'timestamp',
                      key: 'timestamp',
                      width: 180,
                      sorter: (a: SlowQueryRecord, b: SlowQueryRecord) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime(),
                      render: (ts: string) => new Date(ts).toLocaleString(),
                    },
                  ]}
                />
              </Card>
            ),
          },
          {
            key: 'health',
            label: 'Health',
            children: (
              <div>
                <Card
                  title="Container Health"
                  style={{ marginBottom: 16 }}
                  extra={
                    <Button icon={<ReloadOutlined />} loading={healthLoading} onClick={loadHealth}>
                      Refresh
                    </Button>
                  }
                >
                  {health?.message && (
                    <Alert
                      type={health.docker_available ? 'warning' : 'info'}
                      showIcon
                      message={health.message}
                      style={{ marginBottom: 16 }}
                    />
                  )}
                  {health && !health.docker_available ? (
                    <Result
                      status="info"
                      title="Docker health monitoring not configured"
                      subTitle='Deploy the "docker-proxy" sidecar alongside the API (see docker-compose.yml / docker-stack.app.yml) to see container status here.'
                    />
                  ) : health?.services ? (
                    <Table
                      rowKey="name"
                      loading={healthLoading}
                      dataSource={health.services}
                      pagination={false}
                      tableLayout="fixed"
                      scroll={{ x: 'max-content' }}
                      columns={[
                        { title: 'Service', dataIndex: 'name', key: 'name' },
                        { title: 'Mode', dataIndex: 'mode', key: 'mode', width: 110 },
                        { title: 'Image', dataIndex: 'image', key: 'image', ellipsis: true },
                        {
                          title: 'Replicas',
                          key: 'replicas',
                          width: 140,
                          render: (_: unknown, s) => (
                            <Tag color={s.replicas_running >= s.replicas_desired && s.replicas_desired > 0 ? 'green' : 'orange'}>
                              {s.replicas_running} / {s.replicas_desired}
                            </Tag>
                          ),
                        },
                      ]}
                      expandable={{
                        rowExpandable: (s) => s.tasks.length > 0,
                        expandedRowRender: (s) => (
                          <Table
                            rowKey={(t, i) => `${s.name}-${i}`}
                            dataSource={s.tasks}
                            pagination={false}
                            size="small"
                            columns={[
                              { title: 'Node', dataIndex: 'node', key: 'node' },
                              {
                                title: 'State',
                                dataIndex: 'state',
                                key: 'state',
                                render: (state: string) => <Tag color={containerStateColor(state)}>{state}</Tag>,
                              },
                              { title: 'Message', dataIndex: 'status', key: 'status', ellipsis: true },
                            ]}
                          />
                        ),
                      }}
                    />
                  ) : (
                    <Table
                      rowKey="name"
                      loading={healthLoading}
                      dataSource={health?.containers || []}
                      pagination={false}
                      tableLayout="fixed"
                      scroll={{ x: 'max-content' }}
                      columns={[
                        { title: 'Container', dataIndex: 'name', key: 'name' },
                        { title: 'Image', dataIndex: 'image', key: 'image', ellipsis: true },
                        {
                          title: 'State',
                          dataIndex: 'state',
                          key: 'state',
                          width: 120,
                          render: (state: string) => <Tag color={containerStateColor(state)}>{state}</Tag>,
                        },
                        {
                          title: 'Health',
                          dataIndex: 'health',
                          key: 'health',
                          width: 140,
                          render: (h: string) => h ? <Tag color={containerHealthColor(h)}>{h}</Tag> : <span style={{ color: '#999' }}>-</span>,
                        },
                        { title: 'Status', dataIndex: 'status', key: 'status', ellipsis: true },
                      ]}
                    />
                  )}
                </Card>

                <Card
                  title="Syslog Relay Liveness"
                  extra={
                    <Tooltip title="The relay host sits in a separate, untrusted client VLAN with no inbound access and no shared Docker network - real container status can't be checked. This instead shows whether logs are still arriving from its whitelisted IP and whether its certificate is still valid.">
                      <span style={{ color: '#999', cursor: 'help' }}>Why not container status?</span>
                    </Tooltip>
                  }
                >
                  <Table
                    rowKey="ip_address"
                    loading={healthLoading}
                    dataSource={health?.relays || []}
                    pagination={false}
                    tableLayout="fixed"
                    scroll={{ x: 'max-content' }}
                    locale={{ emptyText: 'No relays configured - see Admin > Syslog Relay' }}
                    columns={[
                      { title: 'Label', dataIndex: 'label', key: 'label' },
                      { title: 'IP Address', dataIndex: 'ip_address', key: 'ip_address', width: 160 },
                      {
                        title: 'Certificate',
                        dataIndex: 'cert_status',
                        key: 'cert_status',
                        width: 120,
                        render: (s: string) => <Tag color={s === 'issued' ? 'green' : s === 'revoked' ? 'red' : 'default'}>{s}</Tag>,
                      },
                      {
                        title: 'Last Seen',
                        key: 'last_seen',
                        width: 200,
                        render: (_: unknown, r) => r.last_seen ? new Date(r.last_seen).toLocaleString() : 'never',
                      },
                      {
                        title: 'Status',
                        dataIndex: 'status',
                        key: 'status',
                        width: 140,
                        render: (s: string) => <Tag color={relayStatusColor(s)}>{relayStatusLabel(s)}</Tag>,
                      },
                    ]}
                  />
                </Card>
              </div>
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
        open={!!editUser}
        onCancel={() => setEditUser(null)}
        onOk={handleEditSave}
        okText="Save"
        cancelText="Cancel"
        width={{ sm: '90%', md: 500 }}
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

      <Modal
        title="Edit Device Name"
        open={!!editDevice}
        onCancel={() => setEditDevice(null)}
        onOk={handleEditDeviceSave}
        okText="Save"
        cancelText="Cancel"
        width={{ sm: '90%', md: 500 }}
      >
        <Form form={editDeviceForm} layout="vertical">
          <Form.Item label="Source IP">
            <Input value={editDevice?.fromhost_ip} disabled />
          </Form.Item>
          <Form.Item name="display_name" label="Display Name" rules={[{ required: true, message: 'Required' }]}>
            <Input />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

function containerStateColor(state: string): string {
  switch (state) {
    case 'running': return 'green'
    case 'restarting': return 'orange'
    case 'exited':
    case 'dead': return 'red'
    default: return 'default'
  }
}

function containerHealthColor(health: string): string {
  if (health === 'healthy') return 'green'
  if (health === 'unhealthy') return 'red'
  if (health.startsWith('health:')) return 'processing'
  return 'default'
}

function relayStatusColor(status: string): string {
  switch (status) {
    case 'online': return 'green'
    case 'stale': return 'orange'
    case 'cert_revoked': return 'red'
    default: return 'default'
  }
}

function relayStatusLabel(status: string): string {
  switch (status) {
    case 'online': return 'Online'
    case 'stale': return 'Stale'
    case 'cert_revoked': return 'Certificate Revoked'
    default: return 'Never Seen'
  }
}