import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Layout, Card, Form, Input, Button, message, Typography, Steps, Space, Divider, Switch, Checkbox, Spin, Select, Alert } from 'antd'
import { UploadOutlined, CheckCircleFilled, CloseCircleFilled } from '@ant-design/icons'
import { checkInitialized, initialize, getDbConfig, testDbConfig, testLDAPConnection, InitRequest } from '../services/api'
import { getErrorMessage } from '../utils/error'

const { Title, Text } = Typography
const { Step } = Steps

export default function SetupWizard() {
  const [form] = Form.useForm()
  const [current, setCurrent] = useState(0)
  const [loading, setLoading] = useState(false)
  const [configLoaded, setConfigLoaded] = useState(false)
  const [dbConfigured, setDbConfigured] = useState(false)
  // Whether the server already has JWT_SECRET + ENCRYPTION_KEY in its
  // environment. When true (the expected production case) the wizard skips the
  // key step entirely; when false it shows a blocking notice, since keys are
  // env-only and can't be entered here.
  const [keysConfigured, setKeysConfigured] = useState(true)
  const [dbTestStatus, setDbTestStatus] = useState<'idle' | 'testing' | 'success' | 'failed'>('idle')
  const [dbTestMessage, setDbTestMessage] = useState('')
  const [ldapTestStatus, setLdapTestStatus] = useState<'idle' | 'testing' | 'success' | 'failed'>('idle')
  const [ldapTestMessage, setLdapTestMessage] = useState('')
  const [collectedData, setCollectedData] = useState({
    username: '',
    email: '',
    password: '',
    db_host: '',
    db_port: 0,
    db_name: '',
    db_user: '',
    db_password: '',
    cors_origins: '',
    ldap_enabled: false,
    ldap_server: '',
    ldap_port: 636,
    ldap_use_tls: true,
    ldap_verify_cert: true,
    ldap_ca_cert: '',
    ldap_base_dn: '',
    ldap_bind_dn: '',
    ldap_bind_password: '',
    ldap_user_filter: '',
    ldap_username_attr: '',
    ldap_email_attr: '',
    ldap_auto_provision: false,
    ldap_default_role: 'viewer',
  })
  const navigate = useNavigate()

  useEffect(() => {
    checkInitialized()
      .then((s) => setKeysConfigured(s.keys_configured !== false))
      .catch(() => setKeysConfigured(true))
    getDbConfig().then((dbConfig) => {
      if (!dbConfig.configured && (dbConfig.host || dbConfig.name || dbConfig.user)) {
        form.setFieldsValue({
          db_host: dbConfig.host,
          db_port: dbConfig.port,
          db_name: dbConfig.name,
          db_user: dbConfig.user,
          db_password: dbConfig.password,
        })
        setCollectedData(prev => ({
          ...prev,
          db_host: dbConfig.host || '',
          db_port: dbConfig.port || 0,
          db_name: dbConfig.name || '',
          db_user: dbConfig.user || '',
          db_password: dbConfig.password || '',
        }))
      }
      setDbConfigured(dbConfig.configured)
      setConfigLoaded(true)
    }).catch(() => { setConfigLoaded(true) })
  }, [])

  const allSteps = [
    { key: 'admin', title: 'Admin Account', description: 'Create administrator account' },
    { key: 'database', title: 'Database', description: 'Database connection settings' },
    { key: 'security', title: 'Security Keys', description: 'JWT & encryption keys' },
    { key: 'optional', title: 'Optional Settings', description: 'LDAP & CORS configuration' },
    { key: 'review', title: 'Review & Submit', description: 'Confirm and initialize' },
  ]
  const steps = allSteps.filter(s => {
    if (s.key === 'database' && dbConfigured) return false
    // Security keys come from the server environment. Only surface the step
    // when they're missing (as a blocking notice) - never as an input.
    if (s.key === 'security' && keysConfigured) return false
    return true
  })
  const stepKey = steps[current]?.key
  const lastStep = steps.length - 1

  const handleTestDb = async () => {
    try {
      await form.validateFields(['db_host', 'db_port', 'db_name', 'db_user', 'db_password'])
    } catch {
      message.error('Please fill in all required fields')
      return
    }
    const values = form.getFieldsValue(['db_host', 'db_port', 'db_name', 'db_user', 'db_password'])
    setDbTestStatus('testing')
    setDbTestMessage('')
    try {
      await testDbConfig({
        host: values.db_host,
        port: values.db_port || 0,
        name: values.db_name,
        user: values.db_user,
        password: values.db_password,
      })
      setDbTestStatus('success')
      message.success('Connection successful')
    } catch (e: unknown) {
      setDbTestStatus('failed')
      setDbTestMessage(getErrorMessage(e, 'Connection failed'))
      message.error(getErrorMessage(e, 'Connection failed'))
    }
  }

  const handleTestLdap = async () => {
    try {
      await form.validateFields(['ldap_server', 'ldap_port', 'ldap_base_dn', 'ldap_bind_dn', 'ldap_bind_password'])
    } catch {
      message.error('Please fill in all required LDAP fields')
      return
    }
    const values = form.getFieldsValue(['ldap_server', 'ldap_port', 'ldap_use_tls', 'ldap_verify_cert', 'ldap_ca_cert', 'ldap_base_dn', 'ldap_bind_dn', 'ldap_bind_password'])
    setLdapTestStatus('testing')
    setLdapTestMessage('')
    try {
      await testLDAPConnection({
        server: values.ldap_server,
        port: values.ldap_port || 636,
        use_tls: values.ldap_use_tls,
        verify_cert: values.ldap_verify_cert,
        ca_cert: values.ldap_ca_cert || '',
        base_dn: values.ldap_base_dn,
        bind_dn: values.ldap_bind_dn,
        bind_password: values.ldap_bind_password,
      })
      setLdapTestStatus('success')
      message.success('LDAP connection successful')
    } catch (e: unknown) {
      setLdapTestStatus('failed')
      setLdapTestMessage(getErrorMessage(e, 'LDAP connection failed'))
      message.error(getErrorMessage(e, 'LDAP connection failed'))
    }
  }

  const handleSubmit = async () => {
    const data: InitRequest = {
      admin: {
        username: collectedData.username,
        email: collectedData.email,
        password: collectedData.password,
      },
      database: {
        host: collectedData.db_host || '',
        port: collectedData.db_port || 0,
        name: collectedData.db_name || '',
        user: collectedData.db_user || '',
        password: collectedData.db_password || '',
      },
      cors_origins: collectedData.cors_origins || undefined,
      ldap: collectedData.ldap_enabled ? {
        enabled: collectedData.ldap_enabled,
        server: collectedData.ldap_server,
        port: collectedData.ldap_port || 636,
        use_tls: collectedData.ldap_use_tls,
        verify_cert: collectedData.ldap_verify_cert,
        ca_cert: collectedData.ldap_ca_cert,
        base_dn: collectedData.ldap_base_dn,
        bind_dn: collectedData.ldap_bind_dn,
        bind_password: collectedData.ldap_bind_password,
        user_filter: collectedData.ldap_user_filter,
        username_attr: collectedData.ldap_username_attr,
        email_attr: collectedData.ldap_email_attr,
        auto_provision: collectedData.ldap_auto_provision,
        default_role: collectedData.ldap_default_role,
      } : undefined,
    }

    setLoading(true)
    try {
      await initialize(data)
      message.success('Application initialized! Redirecting...')
      setTimeout(() => { window.location.href = '/login' }, 1000)
    } catch (e: unknown) {
      message.error(getErrorMessage(e, 'Initialization failed'))
      setLoading(false)
    }
  }

  const next = async () => {
    if (stepKey === 'admin') {
      try {
        await form.validateFields(['username', 'email', 'password', 'confirm'])
        const values = form.getFieldsValue(['username', 'email', 'password'])
        setCollectedData(prev => ({ ...prev, ...values }))
        setCurrent(current + 1)
      } catch {
        message.error('Please fill in all required fields')
      }
    } else if (stepKey === 'database') {
      try {
        await form.validateFields(['db_host', 'db_port', 'db_name', 'db_user', 'db_password'])
        if (dbTestStatus !== 'success') {
          message.error('Please test the database connection first')
          return
        }
        const values = form.getFieldsValue(['db_host', 'db_port', 'db_name', 'db_user', 'db_password'])
        setCollectedData(prev => ({ ...prev, ...values, db_port: values.db_port || 0 }))
        setCurrent(current + 1)
      } catch {
        message.error('Please fill in all required fields')
      }
    } else if (stepKey === 'security') {
      // Only reached when keys are missing from the environment; the operator
      // must set them and restart, so there is nothing to advance to.
      message.error('Set JWT_SECRET and ENCRYPTION_KEY in the server environment, then restart')
    } else if (stepKey === 'optional') {
      const values = form.getFieldsValue([
        'cors_origins', 'ldap_enabled', 'ldap_server', 'ldap_port',
        'ldap_use_tls', 'ldap_verify_cert', 'ldap_ca_cert',
        'ldap_base_dn', 'ldap_bind_dn', 'ldap_bind_password',
        'ldap_user_filter', 'ldap_username_attr', 'ldap_email_attr',
        'ldap_auto_provision', 'ldap_default_role',
      ])
      setCollectedData(prev => ({ ...prev, ...values }))
      setCurrent(current + 1)
    } else if (stepKey === 'review') {
      handleSubmit()
    }
  }

  const prev = () => {
    if (current > 0) {
      setCurrent(current - 1)
    }
  }

  const renderStepContent = () => {
    switch (stepKey) {
      case 'admin':
        return (
          <>
            <Form.Item name="username" label="Username" rules={[{ required: true, min: 3 }]}>
              <Input size="large" placeholder="admin" />
            </Form.Item>
            <Form.Item name="email" label="Email" rules={[{ required: true, type: 'email' }]}>
              <Input size="large" placeholder="admin@example.com" />
            </Form.Item>
            <Form.Item name="password" label="Password" rules={[{ required: true, min: 8 }]}>
              <Input.Password size="large" placeholder="Min 8 characters" />
            </Form.Item>
            <Form.Item
              name="confirm"
              label="Confirm Password"
              dependencies={['password']}
              rules={[
                { required: true, message: 'Please confirm your password' },
                ({getFieldValue}) => ({
                  validator(_, value) {
                    if (!value || getFieldValue('password') === value) {
                      return Promise.resolve()
                    }
                    return Promise.reject(new Error('Passwords do not match'))
                  },
                }),
              ]}
            >
              <Input.Password size="large" placeholder="Repeat password" />
            </Form.Item>
          </>
        )
      case 'database':
        return (
          <>
            <Form.Item name="db_host" label="Host" rules={[{ required: true, message: 'Host is required' }]}>
              <Input size="large" placeholder="postgres" />
            </Form.Item>
            <Form.Item name="db_port" label="Port" rules={[{ required: true, message: 'Port is required' }]}>
              <Input type="number" size="large" placeholder="5432" />
            </Form.Item>
            <Form.Item name="db_name" label="Database Name" rules={[{ required: true, message: 'Database name is required' }]}>
              <Input size="large" placeholder="syslog_db" />
            </Form.Item>
            <Form.Item name="db_user" label="User" rules={[{ required: true, message: 'User is required' }]}>
              <Input size="large" placeholder="syslog" />
            </Form.Item>
            <Form.Item name="db_password" label="Password" rules={[{ required: true, message: 'Password is required' }]}>
              <Input.Password size="large" placeholder="syslogpass" />
            </Form.Item>
            <Button
              onClick={handleTestDb}
              loading={dbTestStatus === 'testing'}
              style={{ marginBottom: 8, width: '100%' }}
            >
              Test Connection
            </Button>
            {dbTestStatus === 'success' && (
              <Text type="success" style={{ display: 'block', marginBottom: 16 }}>
                <CheckCircleFilled /> Connection successful
              </Text>
            )}
            {dbTestStatus === 'failed' && (
              <Text type="danger" style={{ display: 'block', marginBottom: 16 }}>
                <CloseCircleFilled /> {dbTestMessage || 'Connection failed'}
              </Text>
            )}
          </>
        )
      case 'security':
        return (
          <Alert
            type="warning"
            showIcon
            message="Security keys are not configured"
            description={
              <>
                <p style={{ marginTop: 0 }}>
                  This server has no <code>JWT_SECRET</code> and/or <code>ENCRYPTION_KEY</code> in its
                  environment. For security these keys are never stored in the database, so they
                  can't be entered here.
                </p>
                <p style={{ marginBottom: 0 }}>
                  Generate each one (e.g. <code>openssl rand -base64 48</code>), set them via the
                  <code> JWT_SECRET</code> and <code>ENCRYPTION_KEY</code> environment variables
                  (or their <code>*_FILE</code> equivalents), restart the server, then reload this
                  page. See the README "Security keys" section.
                </p>
              </>
            }
          />
        )
      case 'optional':
        return (
          <>
            <Form.Item name="cors_origins" label="CORS Origins" tooltip="Comma-separated allowed origins (e.g., http://localhost:3000)">
              <Input size="large" placeholder="http://localhost:3000,https://yourdomain.com" />
            </Form.Item>
            <Divider />
            <Form.Item name="ldap_enabled" label="Enable LDAP Authentication" valuePropName="checked">
              <Switch />
            </Form.Item>
            {form.getFieldValue('ldap_enabled') && (
              <>
                <Divider orientation="left">Connection</Divider>
                <Form.Item name="ldap_server" label="LDAP Server">
                  <Input size="large" placeholder="ldap.example.com" />
                </Form.Item>
                <Form.Item name="ldap_port" label="Port">
                  <Input type="number" size="large" placeholder="636" />
                </Form.Item>
                <Form.Item name="ldap_use_tls" label="Use TLS" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item name="ldap_verify_cert" label="Verify Certificate" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item name="ldap_ca_cert" label="CA Certificate (PEM)">
                  <Input.TextArea rows={2} placeholder="-----BEGIN CERTIFICATE-----..." style={{ resize: 'none' }} />
                </Form.Item>
                <Form.Item>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                    <input
                      type="file"
                      accept=".pem,.crt,.cer"
                      style={{ display: 'none' }}
                      id="ca-cert-upload-wizard"
                      onChange={(e) => {
                        const file = e.target.files?.[0]
                        if (!file) return
                        const reader = new FileReader()
                        reader.onload = (ev) => {
                          const text = ev.target?.result
                          if (typeof text === 'string') {
                            form.setFieldValue('ldap_ca_cert', text)
                            message.success('PEM file loaded')
                          }
                        }
                        reader.onerror = () => message.error('Failed to read PEM file')
                        reader.readAsText(file)
                        e.target.value = ''
                      }}
                    />
                    <Button icon={<UploadOutlined />} block onClick={() => { document.getElementById('ca-cert-upload-wizard')?.click() }}>
                      Upload PEM
                    </Button>
                  </div>
                </Form.Item>
                <Divider orientation="left">Authentication</Divider>
                <Form.Item name="ldap_base_dn" label="Base DN">
                  <Input size="large" placeholder="dc=example,dc=com" />
                </Form.Item>
                <Form.Item name="ldap_bind_dn" label="Bind DN">
                  <Input size="large" placeholder="cn=admin,dc=example,dc=com" />
                </Form.Item>
                <Form.Item name="ldap_bind_password" label="Bind Password">
                  <Input.Password size="large" placeholder="LDAP bind password" />
                </Form.Item>
                <Divider orientation="left">User Search</Divider>
                <Form.Item name="ldap_user_filter" label="User Filter">
                  <Input size="large" placeholder="(uid=%s)" />
                </Form.Item>
                <Form.Item name="ldap_username_attr" label="Username Attribute">
                  <Input size="large" placeholder="uid" />
                </Form.Item>
                <Form.Item name="ldap_email_attr" label="Email Attribute">
                  <Input size="large" placeholder="mail" />
                </Form.Item>
                <Divider orientation="left">Auto-Provisioning</Divider>
                <Form.Item name="ldap_auto_provision" label="Auto-Provision Users" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item name="ldap_default_role" label="Default Role">
                  <Select size="large" placeholder="Select role" options={[
                    { label: 'Viewer', value: 'viewer' },
                    { label: 'Editor', value: 'editor' },
                    { label: 'Admin', value: 'admin' },
                  ]} disabled={!form.getFieldValue('ldap_auto_provision')} />
                </Form.Item>
                <Divider orientation="left">Test</Divider>
                <Button
                  type="primary"
                  onClick={handleTestLdap}
                  loading={ldapTestStatus === 'testing'}
                  style={{ width: '100%' }}
                >
                  Test LDAP Connection
                </Button>
                {ldapTestStatus === 'success' && (
                  <Text type="success" style={{ display: 'block' }}>
                    <CheckCircleFilled /> LDAP connection successful
                  </Text>
                )}
                {ldapTestStatus === 'failed' && (
                  <Text type="danger" style={{ display: 'block' }}>
                    <CloseCircleFilled /> {ldapTestMessage || 'LDAP connection failed'}
                  </Text>
                )}
              </>
            )}
          </>
        )
      case 'review':
        return (
          <Card size="small" style={{ marginBottom: 16 }}>
            <Title level={5}>Admin Account</Title>
            <Text><strong>Username:</strong> {collectedData.username}</Text><br />
            <Text><strong>Email:</strong> {collectedData.email}</Text><br /><br />
            {(collectedData.db_host || collectedData.db_port || collectedData.db_name || collectedData.db_user || collectedData.db_password) && (
              <>
                <Title level={5}>Database</Title>
                {collectedData.db_host && <Text><strong>Host:</strong> {collectedData.db_host}</Text>}
                {collectedData.db_port && <Text> <strong>Port:</strong> {collectedData.db_port}</Text>}
                {collectedData.db_name && <Text><br /><strong>Name:</strong> {collectedData.db_name}</Text>}
                {collectedData.db_user && <Text> <strong>User:</strong> {collectedData.db_user}</Text>}
                <br />
              </>
            )}
            <Title level={5}>Security</Title>
            <Text type="success"><CheckCircleFilled /> JWT &amp; encryption keys loaded from the server environment</Text>
            <br />
            {collectedData.cors_origins && (
              <>
                <Title level={5}>CORS</Title>
                <Text><strong>Origins:</strong> {collectedData.cors_origins}</Text>
                <br />
              </>
            )}
            {collectedData.ldap_enabled && (
              <>
                <Title level={5}>LDAP</Title>
                <Text><strong>Server:</strong> {collectedData.ldap_server}:{collectedData.ldap_port}</Text><br />
                <Text><strong>Base DN:</strong> {collectedData.ldap_base_dn}</Text><br />
                {collectedData.ldap_user_filter && <Text><strong>User Filter:</strong> {collectedData.ldap_user_filter}</Text>}
                {collectedData.ldap_username_attr && <Text> <strong>Username Attr:</strong> {collectedData.ldap_username_attr}</Text>}
                {collectedData.ldap_email_attr && <Text><br /><strong>Email Attr:</strong> {collectedData.ldap_email_attr}</Text>}
                <br />
                <Text><strong>Auto-Provision:</strong> {collectedData.ldap_auto_provision ? 'Yes' : 'No'}</Text>
                {collectedData.ldap_auto_provision && <Text> <strong>Default Role:</strong> {collectedData.ldap_default_role}</Text>}
              </>
            )}
          </Card>
        )
      default:
        return null
    }
  }

  return (
    <Layout style={{ minHeight: '100vh', background: '#f0f2f5', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Card style={{ width: '100%', maxWidth: 600, boxShadow: '0 2px 8px rgba(0,0,0,0.1)' }}>
        <div style={{ textAlign: 'center', marginBottom: 24 }}>
          <Title level={3} style={{ margin: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8 }}>
            <img src="/icons/icon-192.png" alt="Syslytics" style={{ width: 28, height: 28, borderRadius: 6 }} />
            Syslytics
          </Title>
          <Text type="secondary">First-time setup wizard</Text>
        </div>

        {!configLoaded ? (
          <div style={{ display: 'flex', justifyContent: 'center', padding: '40px 0' }}>
            <Spin size="large" />
          </div>
        ) : (
          <>
            <div style={{ overflowX: 'auto', paddingBottom: 8 }}>
              <Steps current={current} style={{ marginBottom: 32 }} items={steps.map(s => ({ title: s.title, description: s.description }))} />
            </div>

            <Form
              form={form}
              layout="vertical"
              onValuesChange={(changed) => {
                if (Object.keys(changed).some(k => k.startsWith('db_'))) {
                  setDbTestStatus('idle')
                }
                if (Object.keys(changed).some(k => k.startsWith('ldap_'))) {
                  setLdapTestStatus('idle')
                }
              }}
            >
              {renderStepContent()}

              <div style={{ display: 'flex', justifyContent: 'space-between', flexWrap: 'wrap', gap: 8 }}>
                <Button disabled={current === 0} onClick={prev} size="large">
                  {current === lastStep ? 'Back' : 'Previous'}
                </Button>
                <Button
                  type="primary"
                  size="large"
                  loading={loading}
                  disabled={stepKey === 'security' || (stepKey === 'database' && dbTestStatus !== 'success') || (stepKey === 'optional' && collectedData.ldap_enabled && ldapTestStatus !== 'success')}
                  onClick={next}
                >
                  {current === lastStep ? 'Initialize' : current === lastStep - 1 ? 'Review' : 'Next'}
                </Button>
              </div>
            </Form>
          </>
        )}
      </Card>
    </Layout>
  )
}