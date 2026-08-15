import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Layout, Card, Form, Input, Button, message, Typography, Steps, Space, Divider, Switch, Checkbox, Spin, Select, Alert } from 'antd'
import { UploadOutlined, CheckCircleFilled, CloseCircleFilled } from '@ant-design/icons'
import { checkInitialized, initialize, getDbConfig, testDbConfig, testLDAPConnection, InitRequest } from '../services/api'
import { getErrorMessage } from '../utils/error'

const { Title, Text } = Typography
const { Step } = Steps

export default function SetupWizard() {
  const { t } = useTranslation()
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
  const ldapEnabled = Form.useWatch('ldap_enabled', form)
  const ldapAutoProvision = Form.useWatch('ldap_auto_provision', form)
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
    { key: 'admin', title: t('setup.adminAccount'), description: t('setup.createAdminAccount') },
    { key: 'database', title: t('setup.database'), description: t('setup.dbConnectionSettings') },
    { key: 'security', title: t('setup.securityKeys'), description: t('setup.jwtEncryptionKeys') },
    { key: 'optional', title: t('setup.optionalSettings'), description: t('setup.ldapCorsConfiguration') },
    { key: 'review', title: t('setup.reviewSubmit'), description: t('setup.confirmAndInitialize') },
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
      message.error(t('setup.fillRequiredFields'))
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
      message.success(t('setup.connectionSuccessful'))
    } catch (e: unknown) {
      setDbTestStatus('failed')
      setDbTestMessage(getErrorMessage(e, t('setup.connectionFailed')))
      message.error(getErrorMessage(e, t('setup.connectionFailed')))
    }
  }

  const handleTestLdap = async () => {
    try {
      await form.validateFields(['ldap_server', 'ldap_port', 'ldap_base_dn', 'ldap_bind_dn', 'ldap_bind_password'])
    } catch {
      message.error(t('setup.fillRequiredLdapFields'))
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
      message.success(t('setup.ldapConnectionSuccessful'))
    } catch (e: unknown) {
      setLdapTestStatus('failed')
      setLdapTestMessage(getErrorMessage(e, t('setup.ldapConnectionFailed')))
      message.error(getErrorMessage(e, t('setup.ldapConnectionFailed')))
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
      message.success(t('setup.initialized'))
      setTimeout(() => { window.location.href = '/login' }, 1000)
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('setup.initFailed')))
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
        message.error(t('setup.fillRequiredFields'))
      }
    } else if (stepKey === 'database') {
      try {
        await form.validateFields(['db_host', 'db_port', 'db_name', 'db_user', 'db_password'])
        if (dbTestStatus !== 'success') {
          message.error(t('setup.testDbFirst'))
          return
        }
        const values = form.getFieldsValue(['db_host', 'db_port', 'db_name', 'db_user', 'db_password'])
        setCollectedData(prev => ({ ...prev, ...values, db_port: values.db_port || 0 }))
        setCurrent(current + 1)
      } catch {
        message.error(t('setup.fillRequiredFields'))
      }
    } else if (stepKey === 'security') {
      // Only reached when keys are missing from the environment; the operator
      // must set them and restart, so there is nothing to advance to.
      message.error(t('setup.setKeysEnv'))
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
            <Form.Item name="username" label={t('setup.username')} rules={[{ required: true, min: 3 }]}>
              <Input size="large" placeholder="admin" />
            </Form.Item>
            <Form.Item name="email" label={t('common.email')} rules={[{ required: true, type: 'email' }]}>
              <Input size="large" placeholder="admin@example.com" />
            </Form.Item>
            <Form.Item name="password" label={t('common.password')} rules={[{ required: true, min: 8 }]}>
              <Input.Password size="large" placeholder={t('setup.min8Chars')} />
            </Form.Item>
            <Form.Item
              name="confirm"
              label={t('setup.confirmPassword')}
              dependencies={['password']}
              rules={[
                { required: true, message: t('setup.confirmPasswordMsg') },
                ({getFieldValue}) => ({
                  validator(_, value) {
                    if (!value || getFieldValue('password') === value) {
                      return Promise.resolve()
                    }
                    return Promise.reject(new Error(t('setup.passwordsNotMatch')))
                  },
                }),
              ]}
            >
              <Input.Password size="large" placeholder={t('setup.repeatPassword')} />
            </Form.Item>
          </>
        )
      case 'database':
        return (
          <>
            <Form.Item name="db_host" label={t('setup.dbHost')} rules={[{ required: true, message: t('setup.dbHostRequired') }]}>
              <Input size="large" placeholder="postgres" />
            </Form.Item>
            <Form.Item name="db_port" label={t('setup.dbPort')} rules={[{ required: true, message: t('setup.dbPortRequired') }]}>
              <Input type="number" size="large" placeholder="5432" />
            </Form.Item>
            <Form.Item name="db_name" label={t('setup.dbName')} rules={[{ required: true, message: t('setup.dbNameRequired') }]}>
              <Input size="large" placeholder="syslog_db" />
            </Form.Item>
            <Form.Item name="db_user" label={t('setup.dbUser')} rules={[{ required: true, message: t('setup.dbUserRequired') }]}>
              <Input size="large" placeholder="syslog" />
            </Form.Item>
            <Form.Item name="db_password" label={t('common.password')} rules={[{ required: true, message: t('setup.dbPassRequired') }]}>
              <Input.Password size="large" placeholder="syslogpass" />
            </Form.Item>
            <Button
              onClick={handleTestDb}
              loading={dbTestStatus === 'testing'}
              style={{ marginBottom: 8, width: '100%' }}
            >
              {t('setup.testConnection')}
            </Button>
            {dbTestStatus === 'success' && (
              <Text type="success" style={{ display: 'block', marginBottom: 16 }}>
                <CheckCircleFilled /> {t('setup.connectionSuccessful')}
              </Text>
            )}
            {dbTestStatus === 'failed' && (
              <Text type="danger" style={{ display: 'block', marginBottom: 16 }}>
                <CloseCircleFilled /> {dbTestMessage || t('setup.connectionFailed')}
              </Text>
            )}
          </>
        )
      case 'security':
        return (
          <Alert
            type="warning"
            showIcon
            message={t('setup.keysNotConfigured')}
            description={
              <>
                <p style={{ marginTop: 0 }}>
                  {t('setup.keysNotConfiguredDesc1')}
                </p>
                <p style={{ marginBottom: 0 }}>
                  {t('setup.keysNotConfiguredDesc2')}
                </p>
              </>
            }
          />
        )
      case 'optional':
        return (
          <>
            <Form.Item name="cors_origins" label={t('setup.corsOrigins')} tooltip={t('setup.corsTooltip')}>
              <Input size="large" placeholder="http://localhost:3000,https://yourdomain.com" />
            </Form.Item>
            <Divider />
            <Form.Item name="ldap_enabled" label={t('setup.enableLdap')} valuePropName="checked">
              <Switch />
            </Form.Item>
            {ldapEnabled && (
              <>
                <Divider orientation="left">{t('setup.connection')}</Divider>
                <Form.Item name="ldap_server" label={t('setup.ldapServer')}>
                  <Input size="large" placeholder="ldap.example.com" />
                </Form.Item>
                <Form.Item name="ldap_port" label={t('setup.dbPort')}>
                  <Input type="number" size="large" placeholder="636" />
                </Form.Item>
                <Form.Item name="ldap_use_tls" label={t('setup.useTls')} valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item name="ldap_verify_cert" label={t('setup.verifyCert')} valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item name="ldap_ca_cert" label={t('setup.caCert')}>
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
                            message.success(t('setup.pemLoaded'))
                          }
                        }
                        reader.onerror = () => message.error(t('setup.pemReadFailed'))
                        reader.readAsText(file)
                        e.target.value = ''
                      }}
                    />
                    <Button icon={<UploadOutlined />} block onClick={() => { document.getElementById('ca-cert-upload-wizard')?.click() }}>
                      {t('setup.uploadPem')}
                    </Button>
                  </div>
                </Form.Item>
                <Divider orientation="left">{t('setup.authentication')}</Divider>
                <Form.Item name="ldap_base_dn" label={t('setup.baseDn')}>
                  <Input size="large" placeholder="dc=example,dc=com" />
                </Form.Item>
                <Form.Item name="ldap_bind_dn" label={t('setup.bindDn')}>
                  <Input size="large" placeholder="cn=admin,dc=example,dc=com" />
                </Form.Item>
                <Form.Item name="ldap_bind_password" label={t('setup.bindPassword')}>
                  <Input.Password size="large" placeholder={t('setup.ldapBindPassword')} />
                </Form.Item>
                <Divider orientation="left">{t('setup.userSearch')}</Divider>
                <Form.Item name="ldap_user_filter" label={t('setup.userFilter')}>
                  <Input size="large" placeholder="(uid=%s)" />
                </Form.Item>
                <Form.Item name="ldap_username_attr" label={t('setup.usernameAttr')}>
                  <Input size="large" placeholder="uid" />
                </Form.Item>
                <Form.Item name="ldap_email_attr" label={t('setup.emailAttr')}>
                  <Input size="large" placeholder="mail" />
                </Form.Item>
                <Divider orientation="left">{t('setup.autoProvisioning')}</Divider>
                <Form.Item name="ldap_auto_provision" label={t('setup.autoProvisionUsers')} valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item name="ldap_default_role" label={t('setup.defaultRole')}>
                  <Select size="large" placeholder={t('setup.selectRole')} options={[
                    { label: t('roles.viewer'), value: 'viewer' },
                    { label: t('roles.editor'), value: 'editor' },
                    { label: t('roles.admin'), value: 'admin' },
                  ]} disabled={!ldapAutoProvision} />
                </Form.Item>
                <Divider orientation="left">{t('common.test')}</Divider>
                <Button
                  type="primary"
                  onClick={handleTestLdap}
                  loading={ldapTestStatus === 'testing'}
                  style={{ width: '100%' }}
                >
                  {t('setup.testLdapConnection')}
                </Button>
                {ldapTestStatus === 'success' && (
                  <Text type="success" style={{ display: 'block' }}>
                    <CheckCircleFilled /> {t('setup.ldapConnectionSuccessful')}
                  </Text>
                )}
                {ldapTestStatus === 'failed' && (
                  <Text type="danger" style={{ display: 'block' }}>
                    <CloseCircleFilled /> {ldapTestMessage || t('setup.ldapConnectionFailed')}
                  </Text>
                )}
              </>
            )}
          </>
        )
      case 'review':
        return (
          <Card size="small" style={{ marginBottom: 16 }}>
            <Title level={5}>{t('setup.adminAccount')}</Title>
            <Text><strong>{t('setup.username')}:</strong> {collectedData.username}</Text><br />
            <Text><strong>{t('common.email')}:</strong> {collectedData.email}</Text><br /><br />
            {(collectedData.db_host || collectedData.db_port || collectedData.db_name || collectedData.db_user || collectedData.db_password) && (
              <>
                <Title level={5}>{t('setup.database')}</Title>
                {collectedData.db_host && <Text><strong>{t('setup.dbHost')}:</strong> {collectedData.db_host}</Text>}
                {collectedData.db_port && <Text> <strong>{t('setup.dbPort')}:</strong> {collectedData.db_port}</Text>}
                {collectedData.db_name && <Text><br /><strong>{t('setup.dbName')}:</strong> {collectedData.db_name}</Text>}
                {collectedData.db_user && <Text> <strong>{t('setup.dbUser')}:</strong> {collectedData.db_user}</Text>}
                <br />
              </>
            )}
            <Title level={5}>{t('setup.security')}</Title>
            <Text type="success"><CheckCircleFilled /> {t('setup.keysLoaded')}</Text>
            <br />
            {collectedData.cors_origins && (
              <>
                <Title level={5}>CORS</Title>
                <Text><strong>{t('setup.origins')}:</strong> {collectedData.cors_origins}</Text>
                <br />
              </>
            )}
            {collectedData.ldap_enabled && (
              <>
                <Title level={5}>LDAP</Title>
                <Text><strong>{t('setup.ldapServer')}:</strong> {collectedData.ldap_server}:{collectedData.ldap_port}</Text><br />
                <Text><strong>{t('setup.baseDn')}:</strong> {collectedData.ldap_base_dn}</Text><br />
                {collectedData.ldap_user_filter && <Text><strong>{t('setup.userFilter')}:</strong> {collectedData.ldap_user_filter}</Text>}
                {collectedData.ldap_username_attr && <Text> <strong>{t('setup.usernameAttr')}:</strong> {collectedData.ldap_username_attr}</Text>}
                {collectedData.ldap_email_attr && <Text><br /><strong>{t('setup.emailAttr')}:</strong> {collectedData.ldap_email_attr}</Text>}
                <br />
                <Text><strong>{t('setup.autoProvision')}:</strong> {collectedData.ldap_auto_provision ? t('common.yes') : t('common.no')}</Text>
                {collectedData.ldap_auto_provision && <Text> <strong>{t('setup.defaultRole')}:</strong> {collectedData.ldap_default_role}</Text>}
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
            <img src="/icons/icon-192.png" alt="Logmara" style={{ width: 28, height: 28, borderRadius: 6 }} />
            Logmara
          </Title>
          <Text type="secondary">{t('setup.wizardTitle')}</Text>
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
                  {current === lastStep ? t('common.back') : t('setup.previous')}
                </Button>
                <Button
                  type="primary"
                  size="large"
                  loading={loading}
                  disabled={stepKey === 'security' || (stepKey === 'database' && dbTestStatus !== 'success') || (stepKey === 'optional' && collectedData.ldap_enabled && ldapTestStatus !== 'success')}
                  onClick={next}
                >
                  {current === lastStep ? t('setup.initialize') : current === lastStep - 1 ? t('setup.review') : t('setup.next')}
                </Button>
              </div>
            </Form>
          </>
        )}
      </Card>
    </Layout>
  )
}