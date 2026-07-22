import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Layout, Card, Form, Input, Button, message, Typography, Steps, Space, Divider, Switch, Checkbox, Spin } from 'antd'
import { UploadOutlined, CheckCircleFilled, CloseCircleFilled } from '@ant-design/icons'
import { generateKeys, initialize, getDbConfig, testDbConfig, InitRequest } from '../services/api'
import { getErrorMessage } from '../utils/error'

const { Title, Text } = Typography
const { Step } = Steps

export default function SetupWizard() {
  const [form] = Form.useForm()
  const [current, setCurrent] = useState(0)
  const [loading, setLoading] = useState(false)
  const [configLoaded, setConfigLoaded] = useState(false)
  const [dbConfigured, setDbConfigured] = useState(false)
  const [dbTestStatus, setDbTestStatus] = useState<'idle' | 'testing' | 'success' | 'failed'>('idle')
  const [dbTestMessage, setDbTestMessage] = useState('')
  const [collectedData, setCollectedData] = useState({
    username: '',
    email: '',
    password: '',
    db_host: '',
    db_port: 0,
    db_name: '',
    db_user: '',
    db_password: '',
    jwt_secret: '',
    encryption_key: '',
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
  })
  const navigate = useNavigate()

  useEffect(() => {
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
  const steps = dbConfigured ? allSteps.filter(s => s.key !== 'database') : allSteps
  const stepKey = steps[current]?.key
  const lastStep = steps.length - 1

  const handleGenerateKeys = async () => {
    setLoading(true)
    try {
      const keys = await generateKeys()
      form.setFieldsValue({
        jwt_secret: keys.jwt_secret,
        encryption_key: keys.encryption_key,
      })
      setCollectedData(prev => ({ ...prev, jwt_secret: keys.jwt_secret, encryption_key: keys.encryption_key }))
      message.success('Keys generated')
    } catch (e) {
      message.error('Failed to generate keys')
    }
    setLoading(false)
  }

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
      jwt_secret: collectedData.jwt_secret,
      encryption_key: collectedData.encryption_key,
      cors_origins: collectedData.cors_origins || undefined,
      ldap: collectedData.ldap_enabled ? {
        server: collectedData.ldap_server,
        port: collectedData.ldap_port || 636,
        use_tls: collectedData.ldap_use_tls,
        verify_cert: collectedData.ldap_verify_cert,
        ca_cert: collectedData.ldap_ca_cert,
        base_dn: collectedData.ldap_base_dn,
        bind_dn: collectedData.ldap_bind_dn,
        bind_password: collectedData.ldap_bind_password,
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
      try {
        await form.validateFields(['jwt_secret', 'encryption_key'])
        const values = form.getFieldsValue(['jwt_secret', 'encryption_key'])
        setCollectedData(prev => ({ ...prev, ...values }))
        setCurrent(current + 1)
      } catch {
        message.error('Please fill in all required fields')
      }
    } else if (stepKey === 'optional') {
      const values = form.getFieldsValue([
        'cors_origins', 'ldap_enabled', 'ldap_server', 'ldap_port',
        'ldap_use_tls', 'ldap_verify_cert', 'ldap_ca_cert',
        'ldap_base_dn', 'ldap_bind_dn', 'ldap_bind_password',
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
          <>
            <Form.Item name="jwt_secret" label="JWT Secret" rules={[{ required: true, min: 16 }]}>
              <Input.Password size="large" placeholder="32+ character secret key" />
            </Form.Item>
            <Form.Item name="encryption_key" label="Encryption Key" rules={[{ required: true, min: 16 }]}>
              <Input.Password size="large" placeholder="32+ character encryption key" />
            </Form.Item>
            <Button
              type="primary"
              onClick={handleGenerateKeys}
              loading={loading}
              style={{ marginBottom: 16, width: '100%' }}
            >
              Generate Both Keys
            </Button>
          </>
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
            <Form.Item name="ldap_base_dn" label="Base DN">
              <Input size="large" placeholder="dc=example,dc=com" />
            </Form.Item>
            <Form.Item name="ldap_bind_dn" label="Bind DN">
              <Input size="large" placeholder="cn=admin,dc=example,dc=com" />
            </Form.Item>
            <Form.Item name="ldap_bind_password" label="Bind Password">
              <Input.Password size="large" placeholder="LDAP bind password" />
            </Form.Item>
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
            <Text><strong>JWT Secret:</strong> {'•'.repeat(Math.min(collectedData.jwt_secret?.length || 0, 32))} ({collectedData.jwt_secret?.length || 0} chars)</Text><br />
            <Text><strong>Encryption Key:</strong> {'•'.repeat(Math.min(collectedData.encryption_key?.length || 0, 32))} ({collectedData.encryption_key?.length || 0} chars)</Text>
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
                <Text><strong>Base DN:</strong> {collectedData.ldap_base_dn}</Text>
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
                  disabled={stepKey === 'database' && dbTestStatus !== 'success'}
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