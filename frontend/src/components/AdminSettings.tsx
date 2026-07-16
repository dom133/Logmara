import { useState, useEffect } from 'react'
import { useAuth } from './auth'
import { api } from './api'
import { Space, Card, Form, InputNumber, Button, Alert, Spin } from 'antd'

export function AdminSettings() {
  const [settings, setSettings] = useState<{ session_timeout: number } | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState(false)

  useEffect(() => {
    fetchSettings()
  }, [])

  const fetchSettings = async () => {
    try {
      setLoading(true)
      const response = await api.get('/admin/settings')
      setSettings(response.data)
      setError(null)
    } catch (err) {
      setError('Failed to load settings')
      console.error('Error loading settings:', err)
    } finally {
      setLoading(false)
    }
  }

  const handleSave = async (values: { session_timeout: number }) => {
    try {
      setLoading(true)
      setSuccess(false)
      setError(null)
      
      await api.post('/admin/settings', values)
      setSettings(values)
      setSuccess(true)
      
      // Refresh the auth context with new timeout value
    } catch (err) {
      setError('Failed to save settings')
      console.error('Error saving settings:', err)
    } finally {
      setLoading(false)
    }
  }

  if (loading) return <Spin size="large" />

  return (
    <Card title="Session Settings">
      {error && <Alert message={error} type="error" showIcon />}
      {success && <Alert message="Settings saved successfully" type="success" showIcon />}
      
      <Form
        initialValues={settings}
        onFinish={handleSave}
        layout="vertical"
      >
        <Form.Item
          label="Session Timeout (seconds)"
          name="session_timeout"
          rules={[{ required: true, message: 'Please enter a timeout value' }]}
        >
          <InputNumber 
            style={{ width: '100%' }} 
            min={30} 
            max={3600}
            placeholder="Session timeout in seconds"
          />
        </Form.Item>
        
        <Form.Item>
          <Space>
            <Button type="primary" htmlType="submit" loading={loading}>
              Save Settings
            </Button>
            <Button onClick={fetchSettings} disabled={loading}>
              Reset
            </Button>
          </Space>
        </Form.Item>
      </Form>
    </Card>
  )
}