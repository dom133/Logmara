import { useEffect, useMemo } from 'react'
import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Layout, Card, Form, Input, Button, Checkbox, message, Typography } from 'antd'
import { useAuth } from '../services/auth'
import { useTheme } from '../App'

const { Title, Text } = Typography

export default function Login() {
  const [loading, setLoading] = useState(false)
  const { login, user } = useAuth()
  const { themeMode } = useTheme()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const redirectPath = useMemo(() => {
    const redirect = searchParams.get('redirect')
    if (redirect && redirect.startsWith('/') && !['/login', '/setup'].includes(redirect)) {
      return redirect
    }
    return '/'
  }, [searchParams])

  useEffect(() => {
    if (user) {
      navigate(redirectPath, { replace: true })
    }
  }, [user, navigate])

  const handleLogin = async (values: { username: string; password: string; remember?: boolean }) => {
    setLoading(true)
    const result = await login(values.username, values.password, values.remember)
    setLoading(false)
    if (result.ok) {
      message.success('Logged in successfully')
      navigate(redirectPath)
    } else {
      message.error(result.error || 'Invalid credentials')
    }
  }

  return (
    <Layout style={{ minHeight: '100vh', background: themeMode === 'dark' ? '#141414' : '#f0f2f5', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Card style={{ width: '100%', maxWidth: 400, boxShadow: '0 2px 8px rgba(0,0,0,0.1)', background: themeMode === 'dark' ? '#1f1f1f' : '#fff' }}>
        <div style={{ textAlign: 'center', marginBottom: 24 }}>
          <Title level={3} style={{ margin: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8 }}>
            <img src="/icons/icon-192.png" alt="Logmara" style={{ width: 28, height: 28, borderRadius: 6 }} />
            Logmara
          </Title>
          <Text type="secondary">Syslog collector & analyzer</Text>
        </div>
        <Form onFinish={handleLogin} layout="vertical">
          <Form.Item name="username" label="Username" rules={[{ required: true }]}>
            <Input size="large" placeholder="Login" />
          </Form.Item>
          <Form.Item name="password" label="Password" rules={[{ required: true }]}>
            <Input.Password size="large" placeholder="Password" />
          </Form.Item>
          <Form.Item name="remember" valuePropName="checked" style={{ marginBottom: 12 }}>
            <Checkbox>Remember this device</Checkbox>
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={loading} size="large" block>
              Login
            </Button>
          </Form.Item>
        </Form>
      </Card>
    </Layout>
  )
}