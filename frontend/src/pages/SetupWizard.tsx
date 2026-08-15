import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Layout, Card, Form, Input, Button, message, Typography, Steps, Space, Divider } from 'antd'
import { generateKeys, initialize, getDbConfig, InitRequest } from '../services/api'

const { Title, Text } = Typography
const { Step } = Steps

export default function SetupWizard() {
  const [form] = Form.useForm()
  const [current, setCurrent] = useState(0)
  const [loading, setLoading] = useState(false)
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
  })
  const navigate = useNavigate()

  useEffect(() => {
    getDbConfig().then((dbConfig) => {
      if (dbConfig.host || dbConfig.name || dbConfig.user) {
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
    }).catch(() => { /* ignore db config errors */ })
  }, [])

  const steps = [
    { title: 'Admin Account', description: 'Create administrator account' },
    { title: 'Database', description: 'Database connection settings' },
    { title: 'Security Keys', description: 'JWT & encryption keys' },
    { title: 'Review & Submit', description: 'Confirm and initialize' },
  ]

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
    }

    setLoading(true)
    try {
      await initialize(data)
      message.success('Application initialized!')
    } catch (e: any) {
      message.error(e?.response?.data?.error || 'Initialization failed')
    }
    setLoading(false)
  }

  const next = async () => {
    if (current === 0) {
      try {
        await form.validateFields(['username', 'email', 'password', 'confirm'])
        const values = form.getFieldsValue(['username', 'email', 'password'])
        setCollectedData(prev => ({ ...prev, ...values }))
        setCurrent(current + 1)
      } catch {
        message.error('Please fill in all required fields')
      }
    } else if (current === 1) {
      try {
        await form.validateFields(['db_host', 'db_port', 'db_name', 'db_user', 'db_password'])
        const values = form.getFieldsValue(['db_host', 'db_port', 'db_name', 'db_user', 'db_password'])
        setCollectedData(prev => ({ ...prev, ...values, db_port: values.db_port || 0 }))
        setCurrent(current + 1)
      } catch {
        message.error('Please fill in all required fields')
      }
    } else if (current === 2) {
      try {
        await form.validateFields(['jwt_secret', 'encryption_key'])
        const values = form.getFieldsValue(['jwt_secret', 'encryption_key'])
        setCollectedData(prev => ({ ...prev, ...values }))
        setCurrent(current + 1)
      } catch {
        message.error('Please fill in all required fields')
      }
    } else if (current === 3) {
      handleSubmit()
    }
  }

  const prev = () => {
    if (current > 0) {
      setCurrent(current - 1)
    }
  }

  const renderStepContent = () => {
    switch (current) {
      case 0:
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
      case 1:
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
          </>
        )
      case 2:
        return (
          <>
            <Form.Item name="jwt_secret" label="JWT Secret" rules={[{ required: true, min: 16 }]}>
              <Input size="large" placeholder="32+ character secret key" />
            </Form.Item>
            <Button
              onClick={handleGenerateKeys}
              loading={loading}
              style={{ marginBottom: 16 }}
            >
              Generate Random Key
            </Button>
            <Divider />
            <Form.Item name="encryption_key" label="Encryption Key" rules={[{ required: true, min: 16 }]}>
              <Input size="large" placeholder="32+ character encryption key" />
            </Form.Item>
            <Button
              onClick={handleGenerateKeys}
              loading={loading}
              style={{ marginBottom: 16 }}
            >
              Generate Random Key
            </Button>
          </>
        )
      case 3:
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
            <Text><strong>JWT Secret:</strong> {collectedData.jwt_secret?.substring(0, 12)}...</Text><br />
            <Text><strong>Encryption Key:</strong> {collectedData.encryption_key?.substring(0, 12)}...</Text>
          </Card>
        )
      default:
        return null
    }
  }

  return (
    <Layout style={{ minHeight: '100vh', background: '#f0f2f5', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Card style={{ width: 600, boxShadow: '0 2px 8px rgba(0,0,0,0.1)' }}>
        <div style={{ textAlign: 'center', marginBottom: 24 }}>
          <Title level={3} style={{ margin: 0 }}>📡 SysLog GUI</Title>
          <Text type="secondary">First-time setup wizard</Text>
        </div>

        <Steps current={current} style={{ marginBottom: 32 }} items={steps.map(s => ({ title: s.title, description: s.description }))} />

        <Form form={form} layout="vertical">
          {renderStepContent()}

          <Space style={{ width: '100%', justifyContent: 'space-between' }}>
            <Button disabled={current === 0} onClick={prev} size="large">
              {current === 3 ? 'Back' : 'Previous'}
            </Button>
            <Button
              type="primary"
              size="large"
              loading={loading}
              onClick={next}
            >
              {current === 3 ? 'Initialize' : current === 2 ? 'Review' : 'Next'}
            </Button>
          </Space>
        </Form>
      </Card>
    </Layout>
  )
}