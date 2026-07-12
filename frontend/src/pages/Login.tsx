import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Layout, Card, Form, Input, Button, Tabs, message, Typography } from 'antd'
import { useAuth } from '../services/auth'

const { Title, Text } = Typography

export default function Login() {
  const [loading, setLoading] = useState(false)
  const { login, register } = useAuth()
  const navigate = useNavigate()

  const handleLogin = async (values: { username: string; password: string }) => {
    setLoading(true)
    const ok = await login(values.username, values.password)
    setLoading(false)
    if (ok) {
      message.success('Logged in successfully')
      navigate('/')
    } else {
      message.error('Invalid credentials')
    }
  }

  const handleRegister = async (values: { username: string; password: string }) => {
    setLoading(true)
    const ok = await register(values.username, values.password)
    setLoading(false)
    if (ok) {
      message.success('Account created')
      navigate('/')
    } else {
      message.error('Registration failed')
    }
  }

  return (
    <Layout style={{ minHeight: '100vh', background: '#f0f2f5', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Card style={{ width: 400, boxShadow: '0 2px 8px rgba(0,0,0,0.1)' }}>
        <div style={{ textAlign: 'center', marginBottom: 24 }}>
          <Title level={3} style={{ margin: 0 }}>📡 SysLog GUI</Title>
          <Text type="secondary">Syslog collector & analyzer</Text>
        </div>
        <Tabs
          defaultActiveKey="login"
          items={[
            {
              key: 'login',
              label: 'Login',
              children: (
                <Form onFinish={handleLogin} layout="vertical">
                  <Form.Item name="username" label="Username" rules={[{ required: true }]}>
                    <Input size="large" placeholder="admin" />
                  </Form.Item>
                  <Form.Item name="password" label="Password" rules={[{ required: true }]}>
                    <Input.Password size="large" placeholder="admin123" />
                  </Form.Item>
                  <Form.Item>
                    <Button type="primary" htmlType="submit" loading={loading} size="large" block>
                      Login
                    </Button>
                  </Form.Item>
                </Form>
              ),
            },
            {
              key: 'register',
              label: 'Register',
              children: (
                <Form onFinish={handleRegister} layout="vertical">
                  <Form.Item name="username" label="Username" rules={[{ required: true }]}>
                    <Input size="large" />
                  </Form.Item>
                  <Form.Item name="password" label="Password" rules={[{ required: true, min: 6 }]}>
                    <Input.Password size="large" />
                  </Form.Item>
                  <Form.Item>
                    <Button type="primary" htmlType="submit" loading={loading} size="large" block>
                      Register
                    </Button>
                  </Form.Item>
                </Form>
              ),
            },
          ]}
        />
      </Card>
    </Layout>
  )
}