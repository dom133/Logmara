import { useState, useEffect, useRef } from 'react'
import { Card, Table, Button, Modal, Form, Input, Select, Switch, Checkbox, Space, Tag, message, Tabs, InputNumber, Divider, Popconfirm, Descriptions, Result, Alert, Tooltip } from 'antd'
import { ThunderboltOutlined, ReloadOutlined, RestOutlined, LoadingOutlined, UploadOutlined, SafetyCertificateOutlined, EyeOutlined, EditOutlined, DeleteOutlined, PlusOutlined, CopyOutlined, KeyOutlined } from '@ant-design/icons'
import { getSettings, updateSettings, cleanupLogs, purgeAllLogs, getDeviceStats, testLDAPConnection, updateDeviceAlias, getSlowQueries, clearSlowQueries, uploadSSLCerts, getContainersHealth, getAuditLogs, getAlerts, getUserDirectory, DeviceStats, SlowQueryRecord, ContainersHealthResponse, AuditLog, AuditLogsResponse, Alert as AlertRule, UserSummary, listAPIKeys, createAPIKey, updateAPIKey, deleteAPIKey, resetAPIKey, APIKey } from '../services/api'
import SeverityTag from '../components/SeverityTag'
import { getErrorMessage } from '../utils/error'
import { useAuth } from '../services/auth'
import AdminUsers from '../components/AdminUsers'
import { containerStateColor } from '../utils/adminUtils'
import { SEVERITY_ORDER, SEVERITY_COLORS, getSeverityLabels } from '../constants'
import { useTranslation } from 'react-i18next'
import i18nInstance, { languageDisplayName, sortLanguagesEnglishFirst } from '../i18n'

const { Option } = Select

function formatDurationAgo(ms: number): string {
  if (ms < 0) ms = 0
  const minutes = Math.floor(ms / 60000)
  if (minutes < 1) return i18nInstance.t('admin.justNow')
  if (minutes < 60) return i18nInstance.t('admin.minutesAgo', { minutes })
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return i18nInstance.t('admin.hoursAgo', { hours, minutes: minutes % 60 })
  const days = Math.floor(hours / 24)
  return i18nInstance.t('admin.daysAgo', { days, hours: hours % 24 })
}

// Loose client-side check (full IPv4/IPv6/CIDR validation happens server-side) -
// this just catches obvious typos before a round trip.
const ipOrCidrPattern = /^[0-9a-fA-F:.]+(\/\d{1,3})?$/

function parseScopeFilters(sf?: { hostnames?: string[]; severities?: string[]; match_mode?: 'and' | 'or' } | null): { hostnames: string[]; severities: string[]; match_mode: 'and' | 'or' } | null {
  if (!sf) return null
  const hostnames = (sf.hostnames || []).map(s => s.trim()).filter(Boolean)
  const severities = (sf.severities || []).map(s => s.trim()).filter(Boolean)
  if (hostnames.length === 0 && severities.length === 0) return null
  return { hostnames, severities, match_mode: sf.match_mode === 'or' ? 'or' : 'and' }
}

export default function Admin() {
  const { refreshUser } = useAuth()
  const { t, i18n } = useTranslation()
  const [settings, setSettings] = useState<Record<string, string>>({})
  const [settingsForm] = Form.useForm()
  const [devices, setDevices] = useState<DeviceStats[]>([])
  const [devicesLoading, setDevicesLoading] = useState(false)
  const [silenceRules, setSilenceRules] = useState<AlertRule[]>([])
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
  const [cleaning, setCleaning] = useState(false)
  const [slowQueries, setSlowQueries] = useState<SlowQueryRecord[]>([])
  const [slowQueriesLoading, setSlowQueriesLoading] = useState(false)
  const [sslUploading, setSslUploading] = useState(false)
  const [certFile, setCertFile] = useState<File | null>(null)
  const [keyFile, setKeyFile] = useState<File | null>(null)
  const [certInfo, setCertInfo] = useState<any>(null)
  const [health, setHealth] = useState<ContainersHealthResponse | null>(null)
  const [healthLoading, setHealthLoading] = useState(false)
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([])
  const [auditLogsTotal, setAuditLogsTotal] = useState(0)
  const [auditLogsLoading, setAuditLogsLoading] = useState(false)
  const [auditLogsOffset, setAuditLogsOffset] = useState(0)
  const [auditLogsFilters, setAuditLogsFilters] = useState<{ username: string; action: string; from: string; to: string }>({ username: '', action: '', from: '', to: '' })
  const [userDirectory, setUserDirectory] = useState<UserSummary[]>([])
  const [auditDetailOpen, setAuditDetailOpen] = useState(false)
  const [auditDetailRecord, setAuditDetailRecord] = useState<AuditLog | null>(null)
  const [activeTab, setActiveTab] = useState('users')
  const tabCacheRef = useRef<Map<string, { loadedAt: number }>>(new Map())

  // API Keys state
  const [apiKeys, setApiKeys] = useState<APIKey[]>([])
  const [apiKeysLoading, setApiKeysLoading] = useState(false)
  const [apiKeyModalOpen, setApiKeyModalOpen] = useState(false)
  const [apiKeyEditing, setApiKeyEditing] = useState<APIKey | null>(null)
  const [apiKeyForm] = Form.useForm()
  const [newKeyDisplay, setNewKeyDisplay] = useState<string | null>(null)

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
      if (data['session_remembered_max_days'] !== undefined && data['session_remembered_max_days'] !== '') formValues['session_remembered_max_days'] = parseInt(data['session_remembered_max_days'], 10)
      if (data['security_max_failed_attempts']) formValues['security_max_failed_attempts'] = parseInt(data['security_max_failed_attempts'], 10)
      if (data['security_lockout_duration_min']) formValues['security_lockout_duration_min'] = parseInt(data['security_lockout_duration_min'], 10)
      if (data['security_password_min_length']) formValues['security_password_min_length'] = parseInt(data['security_password_min_length'], 10)
      if (data['security_password_max_length']) formValues['security_password_max_length'] = parseInt(data['security_password_max_length'], 10)
      formValues['security_password_require_upper'] = data['security_password_require_upper'] === 'true'
      formValues['security_password_require_lower'] = data['security_password_require_lower'] === 'true'
      formValues['security_password_require_digit'] = data['security_password_require_digit'] === 'true'
      formValues['security_password_require_special'] = data['security_password_require_special'] === 'true'
      if (data['security_password_history_count']) formValues['security_password_history_count'] = parseInt(data['security_password_history_count'], 10)
      if (data['security_password_expiry_days']) formValues['security_password_expiry_days'] = parseInt(data['security_password_expiry_days'], 10)
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
      message.error(t('admin.settingsLoadFailed'))
    }
  }

  const handleTestLDAP = async () => {
    const values = settingsForm.getFieldsValue([
      'ldap_server', 'ldap_port', 'ldap_use_tls', 'ldap_verify_cert', 'ldap_ca_cert',
      'ldap_base_dn', 'ldap_bind_dn', 'ldap_bind_password',
    ])
    if (!values.ldap_server) {
      message.warning(t('admin.serverRequired'))
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
      message.success(t('admin.ldapSuccess'))
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('admin.ldapFailed')))
    } finally {
      setTesting(false)
    }
  }

  const loadDevices = async () => {
    setDevicesLoading(true)
    try {
      const [data, alerts] = await Promise.all([
        getDeviceStats(),
        getAlerts().catch(() => [] as AlertRule[]),
      ])
      setDevices(data)
      setSilenceRules(alerts.filter(a => a.rule_type === 'device_silence' && a.is_active !== false))
    } catch {
      message.error(t('admin.devicesLoadFailed'))
    } finally {
      setDevicesLoading(false)
    }
  }

  // Silence rules with an empty device_ips list watch every device; others
  // only watch the IPs listed. Picks the smallest (most sensitive) threshold
  // among the rules that apply to ip, since that's the one that'll fire first.
  const silenceInfoFor = (ip: string): { thresholdMin: number; ruleId: number } | null => {
    let best: { thresholdMin: number; ruleId: number } | null = null
    for (const rule of silenceRules) {
      const applies = !rule.device_ips || rule.device_ips.length === 0 || rule.device_ips.includes(ip)
      if (!applies) continue
      const thresholdMin = rule.threshold > 0 ? rule.threshold : 15
      if (!best || thresholdMin < best.thresholdMin) {
        best = { thresholdMin, ruleId: rule.id }
      }
    }
    return best
  }

  const handleEditDeviceSave = async () => {
    if (!editDevice) return
    const values = editDeviceForm.getFieldsValue()
    try {
      await updateDeviceAlias(editDevice.fromhost_ip, values.display_name)
      message.success(t('admin.deviceAliasUpdated'))
      setEditDevice(null)
      loadDevices()
    } catch {
      message.error(t('admin.aliasUpdateFailed'))
    }
  }

  const loadSlowQueries = async () => {
    setSlowQueriesLoading(true)
    try {
      const data = await getSlowQueries()
      setSlowQueries(data)
    } catch {
      message.error(t('admin.slowQueriesLoadFailed'))
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
      message.error(t('admin.healthLoadFailed'))
    } finally {
      setHealthLoading(false)
    }
  }

  const loadAuditLogs = async (offset: number = 0) => {
    setAuditLogsLoading(true)
    try {
      const data = await getAuditLogs({
        limit: 50,
        offset: offset,
        username: auditLogsFilters.username,
        action: auditLogsFilters.action,
        from: auditLogsFilters.from,
        to: auditLogsFilters.to,
      })
      setAuditLogs(data.data)
      setAuditLogsTotal(data.total)
      setAuditLogsOffset(offset)
    } catch {
      message.error(t('admin.auditLoadFailed'))
    } finally {
      setAuditLogsLoading(false)
    }
  }

  const loadAPIKeys = async () => {
    setApiKeysLoading(true)
    try {
      const data = await listAPIKeys()
      setApiKeys(data)
    } catch {
      message.error(t('admin.apiKeysLoadFailed'))
    } finally {
      setApiKeysLoading(false)
    }
  }

  const handleCreateAPIKey = async () => {
    const values = apiKeyForm.getFieldsValue()
    try {
      const result = await createAPIKey({ ...values, scope_filters: parseScopeFilters(values.scope_filters) })
      setNewKeyDisplay(result.key)
      message.success(t('admin.apiKeyCreated'))
      setApiKeyModalOpen(false)
      setApiKeyEditing(null)
      apiKeyForm.resetFields()
      loadAPIKeys()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('admin.apiKeyCreateFailed')))
    }
  }

  const handleUpdateAPIKey = async () => {
    if (!apiKeyEditing) return
    const values = apiKeyForm.getFieldsValue()
    try {
      await updateAPIKey(apiKeyEditing.id, { ...values, scope_filters: parseScopeFilters(values.scope_filters) })
      message.success(t('admin.apiKeyUpdated'))
      setApiKeyModalOpen(false)
      setApiKeyEditing(null)
      apiKeyForm.resetFields()
      loadAPIKeys()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('admin.apiKeyUpdateFailed')))
    }
  }

  const handleDeleteAPIKey = async (id: number) => {
    try {
      await deleteAPIKey(id)
      message.success(t('admin.apiKeyDeleted'))
      loadAPIKeys()
    } catch {
      message.error(t('admin.apiKeyDeleteFailed'))
    }
  }

  const handleResetAPIKey = async (id: number) => {
    try {
      const result = await resetAPIKey(id)
      setNewKeyDisplay(result.key)
      message.success(t('admin.apiKeyReset'))
      loadAPIKeys()
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('admin.apiKeyResetFailed')))
    }
  }

  const openCreateKeyModal = () => {
    setApiKeyEditing(null)
    apiKeyForm.resetFields()
    apiKeyForm.setFieldsValue({ ttl_days: 30, rate_limit_per_min: 60, scope_filters: { match_mode: 'and' } })
    setApiKeyModalOpen(true)
  }

  const openEditKeyModal = (key: APIKey) => {
    setApiKeyEditing(key)
    apiKeyForm.setFieldsValue({
      name: key.name,
      permissions: key.permissions || {},
      scope_filters: key.scope_filters
        ? {
            hostnames: key.scope_filters.hostnames || [],
            severities: key.scope_filters.severities || [],
            match_mode: key.scope_filters.match_mode === 'or' ? 'or' : 'and',
          }
        : { match_mode: 'and' },
      allowed_ips: key.allowed_ips || [],
      is_active: key.is_active,
      rate_limit_per_min: key.rate_limit_per_min,
      ttl_days: key.expires_at ? Math.max(0, Math.ceil((new Date(key.expires_at).getTime() - Date.now()) / 86400000)) : 0,
    })
    setApiKeyModalOpen(true)
  }

  const handleClearSlowQueries = async () => {
    try {
      await clearSlowQueries()
      message.success(t('admin.slowQueriesCleared'))
      loadSlowQueries()
    } catch {
      message.error(t('admin.clearFailed'))
    }
  }

  const handleUploadSSLCerts = async () => {
    if (!certFile || !keyFile) {
      message.warning(t('admin.bothFilesRequired'))
      return
    }
    setSslUploading(true)
    try {
      const result = await uploadSSLCerts(certFile, keyFile)
      message.success(result.message || t('admin.sslUploaded'))
      setCertFile(null)
      setKeyFile(null)
      if (result.cert_info) {
        setCertInfo(result.cert_info)
      }
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('admin.sslUploadFailed')))
    } finally {
      setSslUploading(false)
    }
  }

  const STALE_MS = 30_000

  const loadTab = async (tab: string) => {
    const cached = tabCacheRef.current.get(tab)
    if (cached && Date.now() - cached.loadedAt < STALE_MS) return
    if (tab === 'ldap' && !tabCacheRef.current.has('settings')) {
      await loadSettings()
      tabCacheRef.current.set('settings', { loadedAt: Date.now() })
    }
    switch (tab) {
      case 'settings': await loadSettings(); break
      case 'devices': await loadDevices(); break
      case 'slow_queries': await loadSlowQueries(); break
      case 'health': await loadHealth(); break
      case 'audit_logs':
        await loadAuditLogs(0)
        getUserDirectory().then(setUserDirectory).catch(() => { /* filter falls back to no options */ })
        break
      case 'api_keys':
        await Promise.all([loadAPIKeys(), devices.length === 0 ? loadDevices() : Promise.resolve()])
        break
    }
    tabCacheRef.current.set(tab, { loadedAt: Date.now() })
  }

  useEffect(() => {
    loadTab(activeTab)
  }, [activeTab])

const handleSaveSettings = async () => {
    const values = settingsForm.getFieldsValue()
    const strValues: Record<string, string> = {}
    for (const [k, v] of Object.entries(values)) {
      strValues[k] = v === undefined || v === null ? '' : String(v)
    }
    try {
      const result = await updateSettings(strValues)
      if (result?.nginx_reload_error) {
        message.warning(t('admin.nginxReloadWarning', { error: result.nginx_reload_error }))
      } else if (result?.relay_reload_error) {
        message.warning(t('admin.relayReloadWarning', { error: result.relay_reload_error }))
      } else {
        message.success(t('admin.settingsSaved'))
      }
      loadSettings()
      // notifications_enabled/relay_ingestion_enabled control which items
      // show in the sidebar (see App.tsx's navItems) - re-fetch /auth/me so
      // that updates immediately instead of only after the next page load.
      refreshUser()
    } catch (e: unknown) {
      const errMsg = getErrorMessage(e, t('admin.settingsSaveFailed'))
      message.error(errMsg)
    }
  }

  const getLdapBool = (key: string) => {
    const v = settingsForm.getFieldValue(key)
    return v === true || v === 'true' || v === 1
  }

const handleCleanup = async () => {
    setCleaning(true)
    try {
      const result = await cleanupLogs()
      message.success(t('admin.logsDeleted', { count: result.deleted_count }))
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('admin.cleanupFailed')))
    } finally {
      setCleaning(false)
    }
  }

  const handlePurgeAll = async () => {
    setPurging(true)
    try {
      const result = await purgeAllLogs(pauseDuringPurge)
      message.success(result.message || t('admin.logsPurged'))
      setPurgeModalOpen(false)
    } catch (e: unknown) {
      message.error(getErrorMessage(e, t('admin.purgeFailed')))
    } finally {
      setPurging(false)
    }
  }

  return (
    <div>
      <Card style={{ marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>{t('admin.title')}</h2>
      </Card>

      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          {
            key: 'users',
            label: t('admin.users'),
            children: <AdminUsers settings={settings} />,
          },
          {
            key: 'settings',
            label: t('admin.settings'),
            children: (
              <Card title={t('admin.appSettings')}>
                <Form form={settingsForm} layout="vertical" onFinish={handleSaveSettings}>
                  <Form.Item label={t('admin.logRetention')} name="retention_days">
                    <InputNumber min={1} max={3650} style={{ width: '100%' }} />
                  </Form.Item>
                    <Form.Item label={t('admin.sessionTimeout')} name="session_timeout_min">
                      <InputNumber min={1} max={10080} style={{ width: '100%' }} />
                    </Form.Item>
                    <Form.Item label={t('admin.rememberedMaxDays')} name="session_remembered_max_days" tooltip={t('admin.rememberedMaxDaysTooltip')}>
                      <InputNumber min={1} max={365} style={{ width: '100%' }} />
                    </Form.Item>
                    <Divider orientation="left">{t('admin.security')}</Divider>
                    <Form.Item label={t('admin.maxFailedAttempts')} name="security_max_failed_attempts" tooltip={t('admin.maxFailedAttemptsTooltip')}>
                      <InputNumber min={1} max={100} style={{ width: '100%' }} />
                    </Form.Item>
                    <Form.Item label={t('admin.lockoutDuration')} name="security_lockout_duration_min" tooltip={t('admin.lockoutDurationTooltip')}>
                      <InputNumber min={1} max={1440} style={{ width: '100%' }} />
                    </Form.Item>
                    <Divider orientation="left">{t('admin.passwordPolicy')}</Divider>
                    <Form.Item label={t('admin.passwordMinLength')} name="security_password_min_length">
                      <InputNumber min={1} max={128} style={{ width: '100%' }} />
                    </Form.Item>
                    <Form.Item label={t('admin.passwordMaxLength')} name="security_password_max_length">
                      <InputNumber min={1} max={256} style={{ width: '100%' }} />
                    </Form.Item>
                    <Form.Item label={t('admin.passwordRequireUpper')} name="security_password_require_upper" valuePropName="checked">
                      <Switch />
                    </Form.Item>
                    <Form.Item label={t('admin.passwordRequireLower')} name="security_password_require_lower" valuePropName="checked">
                      <Switch />
                    </Form.Item>
                    <Form.Item label={t('admin.passwordRequireDigit')} name="security_password_require_digit" valuePropName="checked">
                      <Switch />
                    </Form.Item>
                    <Form.Item label={t('admin.passwordRequireSpecial')} name="security_password_require_special" valuePropName="checked">
                      <Switch />
                    </Form.Item>
                    <Form.Item label={t('admin.passwordHistoryCount')} name="security_password_history_count" tooltip={t('admin.passwordHistoryCountTooltip')}>
                      <InputNumber min={0} max={100} style={{ width: '100%' }} />
                    </Form.Item>
                    <Form.Item label={t('admin.passwordExpiryDays')} name="security_password_expiry_days" tooltip={t('admin.passwordExpiryDaysTooltip')}>
                      <InputNumber min={0} max={3650} style={{ width: '100%' }} />
                    </Form.Item>
                    <Divider orientation="left">{t('admin.cors')}</Divider>
<Form.Item
                      label={t('admin.corsOrigins')}
                      name="cors_origins"
                      tooltip={t('admin.corsOriginsTooltip')}
                    >
                      <Input placeholder="http://localhost:3000,https://yourdomain.com" />
                    </Form.Item>
                    <Divider orientation="left">{t('admin.languageSettings')}</Divider>
                    <Form.Item label={t('admin.defaultLanguage')} name="default_language" tooltip={t('admin.defaultLanguageTooltip')}>
                      <Select
                        options={sortLanguagesEnglishFirst(Object.keys((i18n as any).store?.data || {})).map((l: string) => ({ value: l, label: languageDisplayName(l) }))}
                      />
                    </Form.Item>
                    <Divider orientation="left">{t('admin.https')}</Divider>
                   <Form.Item label={t('admin.enableHttps')} name="https_enabled" valuePropName="checked">
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
                   <Form.Item label={t('admin.redirectHttps')} name="https_redirect" valuePropName="checked">
                     <Switch checked={httpsRedirect} disabled={!httpsEnabled} onChange={(v) => { setHttpsRedirect(v); settingsForm.setFieldValue('https_redirect', v) }} />
                   </Form.Item>
                    <Form.Item label={t('admin.uploadCert')}>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                        <input
                          type="file"
                          accept=".pem,.crt,.cer"
                          style={{ display: 'none' }}
                          id="ssl-cert-upload"
                          disabled={!httpsEnabled}
                          onChange={(e) => {
                            const file = e.target.files?.[0]
                            if (file) { setCertFile(file); message.success(t('admin.certSelected', { filename: file.name })) }
                            e.target.value = ''
                          }}
                        />
                        <Button icon={<UploadOutlined />} disabled={!httpsEnabled} onClick={() => { document.getElementById('ssl-cert-upload')?.click() }}>
                          {certFile ? `${t('admin.selected')}: ${certFile.name}` : t('admin.chooseCert')}
                        </Button>
                      </div>
                    </Form.Item>
                    <Form.Item label={t('admin.uploadKey')}>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                        <input
                          type="file"
                          accept=".key,.pem"
                          style={{ display: 'none' }}
                          id="ssl-key-upload"
                          disabled={!httpsEnabled}
                          onChange={(e) => {
                            const file = e.target.files?.[0]
                            if (file) { setKeyFile(file); message.success(t('admin.keySelected', { filename: file.name })) }
                            e.target.value = ''
                          }}
                        />
                        <Button icon={<UploadOutlined />} disabled={!httpsEnabled} onClick={() => { document.getElementById('ssl-key-upload')?.click() }}>
                          {keyFile ? `${t('admin.selected')}: ${keyFile.name}` : t('admin.chooseKey')}
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
                        {sslUploading ? t('admin.uploading') : t('admin.uploadCerts')}
                      </Button>
                    </Form.Item>
                    {certInfo && (
                      <Result
                        status={certInfo.error ? 'error' : 'success'}
                        icon={<SafetyCertificateOutlined />}
                        title={certInfo.error || t('admin.certVerified')}
                        subTitle={certInfo.subject}
                      >
                        <Descriptions bordered column={1} size="small">
                          <Descriptions.Item label={t('admin.subject')}><span style={{ wordBreak: 'break-all' }}>{certInfo.subject || '-'}</span></Descriptions.Item>
                          <Descriptions.Item label={t('admin.issuer')}><span style={{ wordBreak: 'break-all' }}>{certInfo.issuer || '-'}</span></Descriptions.Item>
                          <Descriptions.Item label={t('admin.validFrom')}>{certInfo.valid_from || '-'}</Descriptions.Item>
                          <Descriptions.Item label={t('admin.validTo')}>{certInfo.valid_to || '-'}</Descriptions.Item>
                          <Descriptions.Item label={t('admin.dnsNames')}><span style={{ wordBreak: 'break-all' }}>{Array.isArray(certInfo.dns_names) && certInfo.dns_names.length > 0 ? certInfo.dns_names.join(', ') : '-'}</span></Descriptions.Item>
                          {certInfo.error && <Descriptions.Item label={t('admin.error')}><span style={{ wordBreak: 'break-all' }}>{certInfo.error}</span></Descriptions.Item>}
                        </Descriptions>
                      </Result>
                    )}
                    <Divider orientation="left">{t('admin.notifications')}</Divider>
                    <Form.Item label={t('admin.enableNotifications')} name="notifications_enabled" valuePropName="checked" tooltip={t('admin.enableNotificationsTooltip')}>
                      <Switch />
                    </Form.Item>
                    <Form.Item label={t('admin.enableSmtp')} name="smtp_enabled" valuePropName="checked" tooltip={t('admin.enableSmtpTooltip')}>
                      <Switch checked={smtpEnabled} onChange={(v) => { setSmtpEnabled(v); settingsForm.setFieldValue('smtp_enabled', v) }} />
                    </Form.Item>
                    <Form.Item label={t('admin.smtpHost')} name="smtp_host">
                      <Input placeholder="smtp.example.com" disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label={t('admin.smtpPort')} name="smtp_port">
                      <InputNumber min={1} max={65535} style={{ width: '100%' }} disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label={t('admin.smtpUsername')} name="smtp_username">
                      <Input placeholder="notifications@example.com" disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label={t('admin.smtpPassword')} name="smtp_password">
                      <Input.Password disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label={t('admin.fromAddress')} name="smtp_from">
                      <Input placeholder="logmara@example.com" disabled={!smtpEnabled} />
                    </Form.Item>
                    <Form.Item label={t('admin.useStarttls')} name="smtp_use_tls" valuePropName="checked">
                      <Switch disabled={!smtpEnabled} />
                    </Form.Item>
                    <Divider orientation="left">{t('admin.syslogRelay')}</Divider>
                    <Form.Item
                      label={t('admin.enableRelayIngestion')}
                      name="relay_ingestion_enabled"
                      valuePropName="checked"
                      tooltip={t('admin.enableRelayIngestionTooltip')}
                    >
                      <Switch checked={relayEnabled} onChange={(v) => { setRelayEnabled(v); settingsForm.setFieldValue('relay_ingestion_enabled', v) }} />
                    </Form.Item>
                    <Form.Item
                      label={t('admin.centralServer')}
                      name="relay_central_host"
                      tooltip={t('admin.centralServerTooltip')}
                    >
                      <Input placeholder="e.g. syslog.example.com or 10.0.0.5" disabled={!relayEnabled} />
                    </Form.Item>
                  <Divider />
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                    <Button type="primary" htmlType="submit">
                      {t('admin.saveSettings')}
                    </Button>
                    <Button danger icon={<ThunderboltOutlined />} onClick={handleCleanup} disabled={cleaning} loading={cleaning}>
                      {t('admin.cleanOldLogs')}
                    </Button>
                    <Button danger type="primary" onClick={() => setPurgeModalOpen(true)}>
                      {t('admin.purgeAllLogs')}
                    </Button>
                    <Modal
                      title={t('admin.purgeConfirm')}
                      open={purgeModalOpen}
                      onOk={handlePurgeAll}
                      onCancel={() => setPurgeModalOpen(false)}
                      okText={t('admin.yesPurge')}
                      cancelText={t('common.cancel')}
                      okButtonProps={{ danger: true, disabled: purging, loading: purging }}
                    >
                      <p>{t('admin.purgeConfirmText')}</p>
                      <div style={{ marginTop: 16 }}>
                        <label style={{ cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 8 }}>
                          <input
                            type="checkbox"
                            checked={pauseDuringPurge}
                            onChange={(e) => setPauseDuringPurge(e.target.checked)}
                          />
                          {t('admin.pauseIngestion')}
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
            label: t('admin.ldap'),
            children: (
              <Card title={t('admin.ldapAuth')}>
                <Form form={settingsForm} layout="vertical" onFinish={handleSaveSettings}>
                  <Form.Item label={t('admin.enableLdap')} name="ldap_enabled" valuePropName="checked">
                    <Switch checked={ldapEnabled} onChange={(v) => { setLdapEnabled(v); settingsForm.setFieldValue('ldap_enabled', v); }} />
                  </Form.Item>
                  <Form.Item label={t('admin.server')} name="ldap_server">
                    <Input placeholder="ldap.example.com" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label={t('admin.port')} name="ldap_port">
                    <InputNumber min={1} max={65535} style={{ width: '100%' }} disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label={t('admin.useTls')} name="ldap_use_tls" valuePropName="checked">
                    <Switch checked={ldapUseTls} disabled={!ldapEnabled} onChange={(v) => { setLdapUseTls(v); settingsForm.setFieldValue('ldap_use_tls', v) }} />
                  </Form.Item>
                  <Divider orientation="left">{t('admin.tlsCert')}</Divider>
                  <Form.Item label={t('admin.verifyTlsCert')} name="ldap_verify_cert" valuePropName="checked">
                    <Switch checked={ldapVerifyCert} disabled={!ldapEnabled} onChange={(v) => { setLdapVerifyCert(v); settingsForm.setFieldValue('ldap_verify_cert', v) }} />
                  </Form.Item>
                  <Form.Item
                    label={t('admin.customCaCert')}
                    name="ldap_ca_cert"
                  >
                    <Input.TextArea rows={4} placeholder={t('admin.pemPlaceholder')} disabled={!ldapEnabled} style={{ resize: 'none' }} />
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
                              message.success(t('admin.pemLoaded'))
                            }
                          }
                          reader.onerror = () => message.error(t('admin.pemReadFailed'))
                          reader.readAsText(file)
                          e.target.value = ''
                        }}
                      />
                      <Button icon={<UploadOutlined />} disabled={!ldapEnabled} block onClick={() => { document.getElementById('ca-cert-upload')?.click() }}>
                        {t('admin.uploadPem')}
                      </Button>
                    </div>
                  </Form.Item>
                  <Divider orientation="left">{t('admin.connection')}</Divider>
                  <Form.Item label={t('admin.baseDN')} name="ldap_base_dn">
                    <Input placeholder="dc=example,dc=com" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label={t('admin.bindDN')} name="ldap_bind_dn">
                    <Input placeholder="cn=admin,dc=example,dc=com" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label={t('admin.bindPassword')} name="ldap_bind_password">
                    <Input.Password disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label={t('admin.userFilter')} name="ldap_user_filter">
                    <Input placeholder="(uid=%s)" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Divider orientation="left">{t('admin.attributeMapping')}</Divider>
                  <Form.Item label={t('admin.usernameAttr')} name="ldap_username_attr">
                    <Input placeholder="uid" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label={t('admin.emailAttr')} name="ldap_email_attr">
                    <Input placeholder="mail" disabled={!ldapEnabled} />
                  </Form.Item>
                  <Form.Item label={t('admin.autoProvision')} name="ldap_auto_provision" valuePropName="checked">
                    <Switch disabled={!ldapEnabled} onChange={(v) => { setLdapAutoProvision(v); settingsForm.setFieldValue('ldap_auto_provision', v); }} />
                  </Form.Item>
                  <Form.Item label={t('admin.defaultRole')} name="ldap_default_role">
                    <Select style={{ width: '100%' }} disabled={!ldapEnabled || !ldapAutoProvision}>
                      <Option value="viewer">{t('admin.viewer')}</Option>
                      <Option value="editor">{t('admin.editor')}</Option>
                      <Option value="admin">{t('admin.admin')}</Option>
                    </Select>
                  </Form.Item>
                  <div style={{ display: 'flex', gap: 8 }}>
                    <Button type="primary" htmlType="submit" disabled={!ldapEnabled}>
                      {t('admin.saveLdapSettings')}
                    </Button>
                    <Button
                      icon={testing ? <LoadingOutlined /> : undefined}
                      onClick={handleTestLDAP}
                      disabled={!ldapEnabled || testing}
                    >
                      {t('admin.testConnection')}
                    </Button>
                  </div>
                </Form>
              </Card>
            ),
          },
          {
            key: 'devices',
            label: t('admin.devices'),
            children: (
              <Card
                title={t('admin.deviceStats')}
                extra={
                  <Button icon={<ReloadOutlined />} onClick={loadDevices}>
                    {t('common.refresh')}
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
                      title: t('admin.sourceIp'),
                      dataIndex: 'fromhost_ip',
                      key: 'fromhost_ip',
                      width: 140,
                      render: (ip: string) => ip || '-',
                    },
                    {
                      title: t('admin.hostname'),
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
                      title: t('admin.proxy'),
                      dataIndex: 'via_relay',
                      key: 'via_relay',
                      width: 160,
                      filters: [
                        { text: t('admin.viaRelay'), value: true },
                        { text: t('admin.direct'), value: false },
                      ],
                      onFilter: (value, record: DeviceStats) => record.uses_proxy === value,
                      render: (_v: unknown, record: DeviceStats) =>
                        record.uses_proxy
                          ? <Tag color="blue">{record.via_relay}</Tag>
                          : <span style={{ color: '#999' }}>{t('admin.direct')}</span>,
                    },
                    {
                      title: t('admin.totalLogs'),
                      dataIndex: 'total_logs',
                      key: 'total_logs',
                      width: 100,
                      sorter: (a: DeviceStats, b: DeviceStats) => a.total_logs - b.total_logs,
                      render: (v: number) => typeof v === 'number' ? v : 0,
                    },
                    {
                      title: t('admin.lastSeen'),
                      dataIndex: 'last_seen',
                      key: 'last_seen',
                      width: 200,
                      render: (date: string) => {
                        if (!date) return '-'
                        const ago = formatDurationAgo(Date.now() - new Date(date).getTime())
                        return (
                          <span>
                            {new Date(date).toLocaleString()}
                            <br />
                            <span style={{ color: '#999', fontSize: 12 }}>{ago}</span>
                          </span>
                        )
                      },
                      sorter: (a: DeviceStats, b: DeviceStats) => new Date(a.last_seen).getTime() - new Date(b.last_seen).getTime(),
                    },
                    {
                      title: t('admin.silenceStatus'),
                      key: 'silence_status',
                      width: 160,
                      render: (_v: unknown, record: DeviceStats) => {
                        const info = silenceInfoFor(record.fromhost_ip)
                        if (!info) {
                          return <Tag>{t('admin.noRule')}</Tag>
                        }
                        const minutesSinceLastSeen = record.last_seen
                          ? (Date.now() - new Date(record.last_seen).getTime()) / 60000
                          : Infinity
                        const isSilent = minutesSinceLastSeen >= info.thresholdMin
                        return (
                          <Tooltip title={t('admin.silenceTooltip', { threshold: info.thresholdMin })}>
                            <a onClick={() => window.location.href = '/alerts'}>
                              <Tag color={isSilent ? 'red' : 'green'}>{isSilent ? t('admin.silent') : t('admin.ok')}</Tag>
                            </a>
                          </Tooltip>
                        )
                      },
                    },
                    {
                      title: t('admin.severity'),
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
                      title: t('admin.matchedParsers'),
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
                      title: t('admin.parsed'),
                      dataIndex: 'has_parsed',
                      key: 'has_parsed',
                      width: 90,
                      render: (parsed: boolean) => (
                        <Tag color={parsed ? 'green' : 'orange'}>
                          {parsed ? t('common.yes') : t('common.no')}
                        </Tag>
                      ),
                    },
                    {
                      title: t('common.actions'),
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
                          {t('admin.editName')}
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
            label: t('admin.slowQueries'),
            children: (
              <Card
                title={t('admin.slowQueryLog')}
                extra={
                  <div style={{ display: 'flex', gap: 8 }}>
                    <Button icon={<ReloadOutlined />} onClick={loadSlowQueries}>
                      {t('common.refresh')}
                    </Button>
                    <Button danger icon={<DeleteOutlined />} onClick={handleClearSlowQueries}>
                      {t('common.clear')}
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
                      title: t('admin.query'),
                      dataIndex: 'name',
                      key: 'name',
                      width: 400,
                      render: (name: string) => (
                        <Tag color="orange" style={{ whiteSpace: 'normal', wordBreak: 'break-all', maxWidth: '100%' }}>{name}</Tag>
                      ),
                    },
                    {
                      title: t('admin.duration'),
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
                      title: t('admin.timestamp'),
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
            label: t('admin.health'),
            children: (
              <div>
                <Card
                  title={t('admin.containerHealth')}
                  style={{ marginBottom: 16 }}
                  extra={
                    <Button icon={<ReloadOutlined />} loading={healthLoading} onClick={loadHealth}>
                      {t('common.refresh')}
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
                      title={t('admin.dockerNotConfigured')}
                      subTitle={t('admin.dockerNotConfiguredSub')}
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
                        { title: t('admin.service'), dataIndex: 'name', key: 'name' },
                        { title: t('admin.mode'), dataIndex: 'mode', key: 'mode', width: 110 },
                        { title: t('admin.image'), dataIndex: 'image', key: 'image', ellipsis: true },
                        {
                          title: t('admin.replicas'),
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
                              { title: t('admin.node'), dataIndex: 'node', key: 'node' },
                              {
                                title: t('admin.state'),
                                dataIndex: 'state',
                                key: 'state',
                                render: (state: string) => <Tag color={containerStateColor(state)}>{state}</Tag>,
                              },
                              { title: t('admin.message'), dataIndex: 'status', key: 'status', ellipsis: true },
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
                        { title: t('admin.container'), dataIndex: 'name', key: 'name' },
                        { title: t('admin.image'), dataIndex: 'image', key: 'image', ellipsis: true },
                        {
                          title: t('admin.state'),
                          dataIndex: 'state',
                          key: 'state',
                          width: 120,
                          render: (state: string) => <Tag color={containerStateColor(state)}>{state}</Tag>,
                        },
                        {
                          title: t('admin.healthStatus'),
                          dataIndex: 'health',
                          key: 'health',
                          width: 140,
                          render: (h: string) => h ? <Tag color={containerHealthColor(h)}>{h}</Tag> : <span style={{ color: '#999' }}>-</span>,
                        },
                        { title: t('admin.status'), dataIndex: 'status', key: 'status', ellipsis: true },
                      ]}
                    />
                  )}
                </Card>

                <Card
                  title={t('admin.relayLiveness')}
                  extra={
                    <Tooltip title={t('admin.relayLivenessTooltip')}>
                      <span style={{ color: '#999', cursor: 'help' }}>{t('admin.whyNotContainerStatus')}</span>
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
                    locale={{ emptyText: t('admin.noRelays') }}
                    columns={[
                      { title: t('admin.label'), dataIndex: 'label', key: 'label' },
                      { title: t('admin.ipAddress'), dataIndex: 'ip_address', key: 'ip_address', width: 160 },
                      {
                        title: t('admin.certificate'),
                        dataIndex: 'cert_status',
                        key: 'cert_status',
                        width: 120,
                        render: (s: string) => <Tag color={s === 'issued' ? 'green' : s === 'revoked' ? 'red' : 'default'}>{s}</Tag>,
                      },
                      {
                        title: t('admin.lastSeen'),
                        key: 'last_seen',
                        width: 200,
                        render: (_: unknown, r) => r.last_seen ? new Date(r.last_seen).toLocaleString() : t('admin.never'),
                      },
                      {
                        title: t('common.status'),
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
          {
            key: 'audit_logs',
            label: t('admin.auditLogs'),
            children: (
              <Card
                title={t('admin.auditLogsTitle')}
                extra={
                  <Button icon={<ReloadOutlined />} loading={auditLogsLoading} onClick={() => loadAuditLogs(0)}>
                    {t('common.refresh')}
                  </Button>
                }
              >
                <Space style={{ marginBottom: 16 }} wrap>
                  <Select
                    placeholder={t('admin.filterByUser')}
                    value={auditLogsFilters.username || undefined}
                    style={{ width: 180 }}
                    allowClear
                    showSearch
                    onChange={(val) => { setAuditLogsFilters({ ...auditLogsFilters, username: val || '' }); loadAuditLogs(0) }}
                    options={userDirectory.map((u) => ({ label: u.username, value: u.username }))}
                  />
                  <Select
                    placeholder={t('admin.filterByAction')}
                    value={auditLogsFilters.action || undefined}
                    style={{ width: 200 }}
                    allowClear
                    onChange={(val) => { setAuditLogsFilters({ ...auditLogsFilters, action: val || '' }); loadAuditLogs(0) }}
                    options={[
                      { label: 'login', value: 'login' },
                      { label: 'logout', value: 'logout' },
                      { label: 'create_user', value: 'create_user' },
                      { label: 'update_user', value: 'update_user' },
                      { label: 'delete_user', value: 'delete_user' },
                      { label: 'reset_password', value: 'reset_password' },
                      { label: 'update_settings', value: 'update_settings' },
                      { label: 'cleanup_logs', value: 'cleanup_logs' },
                      { label: 'purge_all_logs', value: 'purge_all_logs' },
                    ]}
                  />
                  <Input
                    type="datetime-local"
                    value={auditLogsFilters.from}
                    onChange={(e) => setAuditLogsFilters({ ...auditLogsFilters, from: e.target.value })}
                    style={{ width: 220 }}
                  />
                  <Input
                    type="datetime-local"
                    value={auditLogsFilters.to}
                    onChange={(e) => setAuditLogsFilters({ ...auditLogsFilters, to: e.target.value })}
                    style={{ width: 220 }}
                  />
                  <Button type="primary" onClick={() => loadAuditLogs(0)}>{t('admin.apply')}</Button>
                  <Button onClick={() => { setAuditLogsFilters({ username: '', action: '', from: '', to: '' }); loadAuditLogs(0) }}>{t('common.clear')}</Button>
                </Space>
                <Table
                  rowKey="id"
                  columns={[
                    { title: t('admin.timestamp'), dataIndex: 'created_at', key: 'created_at', width: 200, render: (v: string) => new Date(v).toLocaleString() },
                    { title: t('notifDetail.user'), dataIndex: 'username', key: 'username', width: 150 },
                    { title: t('notifDetail.action'), dataIndex: 'action', key: 'action', width: 160, render: (v: string) => <Tag>{v.replace(/_/g, ' ')}</Tag> },
                    { title: t('notifDetail.ip'), dataIndex: 'ip', key: 'ip', width: 140, render: (v: string | null) => v || '-' },
                    {
                      title: t('common.details'),
                      key: 'details',
                      width: 200,
                      ellipsis: true,
                      render: (_: unknown, record: AuditLog) => (
                        <Button type="link" size="small" icon={<EyeOutlined />} onClick={() => { setAuditDetailRecord(record); setAuditDetailOpen(true) }}>
                          {record.details || '-'}
                        </Button>
                      ),
                    },
                  ]}
                  dataSource={auditLogs}
                  loading={auditLogsLoading}
                  pagination={{
                    current: Math.floor(auditLogsOffset / 50) + 1,
                    pageSize: 50,
                    total: auditLogsTotal,
                    showSizeChanger: false,
                    showTotal: (total) => t('admin.auditEntries', { count: total }),
                  }}
                  onChange={(pag) => { if (pag.current) loadAuditLogs((pag.current - 1) * 50) }}
                  tableLayout="fixed"
                  scroll={{ x: 'max-content'                   }}
                />
              </Card>
            ),
          },
          {
            key: 'api_keys',
            label: t('admin.apiKeys'),
            children: (
              <Card
                title={t('admin.apiKeysTitle')}
                extra={
                  <Button type="primary" icon={<PlusOutlined />} onClick={openCreateKeyModal}>
                    {t('admin.createApiKey')}
                  </Button>
                }
              >
                <Table
                  loading={apiKeysLoading}
                  rowKey="id"
                  dataSource={apiKeys}
                  pagination={false}
                  tableLayout="fixed"
                  scroll={{ x: 'max-content' }}
                  columns={[
                    {
                      title: t('admin.name'),
                      dataIndex: 'name',
                      key: 'name',
                      width: 150,
                    },
                    {
                      title: t('admin.keyPrefix'),
                      dataIndex: 'keyPrefix',
                      key: 'keyPrefix',
                      width: 120,
                      render: (v: string) => <Tag>{v}</Tag>,
                    },
                    {
                      title: t('admin.permissions'),
                      dataIndex: 'permissions',
                      key: 'permissions',
                      width: 200,
                      render: (p: Record<string, boolean>) => (
                        <Space wrap>
                          {p?.export_json && <Tag color="blue">{t('admin.permExportJson')}</Tag>}
                          {p?.export_parsed && <Tag color="purple">{t('admin.permExportParsed')}</Tag>}
                          {p?.view_stats && <Tag color="cyan">{t('admin.permViewStats')}</Tag>}
                        </Space>
                      ),
                    },
                    {
                      title: t('admin.scopeFilters'),
                      dataIndex: 'scope_filters',
                      key: 'scope_filters',
                      width: 200,
                      render: (sf: { hostnames?: string[]; severities?: string[]; match_mode?: 'and' | 'or' }) => {
                        if (!sf) return <span>-</span>
                        const parts: string[] = []
                        if (sf.hostnames?.length) parts.push(t('admin.scopeHosts', { count: sf.hostnames.length }))
                        if (sf.severities?.length) parts.push(t('admin.scopeSevs', { count: sf.severities.length }))
                        const joiner = sf.match_mode === 'or' ? ` ${t('admin.scopeMatchModeOr')} ` : ` ${t('admin.scopeMatchModeAnd')} `
                        return parts.length ? <Tag>{parts.join(parts.length > 1 ? joiner : ', ')}</Tag> : <span>-</span>
                      },
                    },
                    {
                      title: t('admin.allowedIps'),
                      dataIndex: 'allowed_ips',
                      key: 'allowed_ips',
                      width: 150,
                      render: (ips: string[] | undefined) =>
                        ips?.length ? <Tag>{t('admin.scopeIps', { count: ips.length })}</Tag> : <span>-</span>,
                    },
                    {
                      title: t('admin.rateLimit'),
                      dataIndex: 'rate_limit_per_min',
                      key: 'rate_limit_per_min',
                      width: 100,
                      render: (v: number) => `${v}/min`,
                    },
                    {
                      title: t('admin.expires'),
                      dataIndex: 'expires_at',
                      key: 'expires_at',
                      width: 150,
                      render: (v: string | null) => v ? new Date(v).toLocaleDateString() : <Tag color="green">{t('admin.never')}</Tag>,
                    },
                    {
                      title: t('admin.active'),
                      dataIndex: 'is_active',
                      key: 'is_active',
                      width: 80,
                      render: (v: boolean) => <Tag color={v ? 'green' : 'red'}>{v ? t('common.yes') : t('common.no')}</Tag>,
                    },
                    {
                      title: t('admin.createdBy'),
                      dataIndex: 'created_by',
                      key: 'created_by',
                      width: 120,
                    },
                    {
                      title: t('admin.usage'),
                      dataIndex: 'total_requests',
                      key: 'total_requests',
                      width: 100,
                      render: (v: number) => v.toLocaleString(),
                    },
                    {
                      title: t('common.actions'),
                      key: 'actions',
                      width: 200,
                      render: (_: unknown, record: APIKey) => (
                        <Space>
                          <Button type="link" size="small" icon={<EditOutlined />} onClick={() => openEditKeyModal(record)}>
                            {t('common.edit')}
                          </Button>
                          <Popconfirm
                            title={t('admin.revokeConfirm')}
                            onConfirm={() => handleDeleteAPIKey(record.id)}
                            okText={t('common.yes')}
                            cancelText={t('common.no')}
                          >
                            <Button type="link" size="small" danger icon={<DeleteOutlined />}>
                              {t('admin.revoke')}
                            </Button>
                          </Popconfirm>
                          <Popconfirm
                            title={t('admin.resetConfirm')}
                            onConfirm={() => handleResetAPIKey(record.id)}
                            okText={t('common.yes')}
                            cancelText={t('common.no')}
                          >
                            <Button type="link" size="small" icon={<RestOutlined />}>
                              {t('admin.regenerate')}
                            </Button>
                          </Popconfirm>
                        </Space>
                      ),
                    },
                  ]}
                />
              </Card>
            ),
          },
        ]}
      />


      <Modal
        title={t('admin.editDeviceName')}
        open={!!editDevice}
        onCancel={() => setEditDevice(null)}
        onOk={handleEditDeviceSave}
        okText={t('common.save')}
        cancelText={t('common.cancel')}
        width={{ sm: '90%', md: 500 }}
      >
        <Form form={editDeviceForm} layout="vertical">
          <Form.Item label={t('admin.sourceIp')}>
            <Input value={editDevice?.fromhost_ip} disabled />
          </Form.Item>
          <Form.Item name="display_name" label={t('admin.displayName')} rules={[{ required: true, message: t('relay.required') }]}>
            <Input />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={t('admin.auditLogDetails')}
        open={auditDetailOpen}
        onCancel={() => setAuditDetailOpen(false)}
        footer={[
          <Button key="close" onClick={() => setAuditDetailOpen(false)} style={{ width: '100%' }}>{t('common.close')}</Button>
        ]}
        width={{ sm: '90%', md: 700 }}
      >
        {auditDetailRecord && (
          <div style={{ overflowX: 'auto', maxWidth: '100%' }}>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label={t('admin.timestamp')}>{new Date(auditDetailRecord.created_at).toLocaleString()}</Descriptions.Item>
              <Descriptions.Item label={t('notifDetail.user')}>{auditDetailRecord.username}</Descriptions.Item>
              <Descriptions.Item label={t('notifDetail.action')}>{auditDetailRecord.action.replace(/_/g, ' ')}</Descriptions.Item>
              <Descriptions.Item label={t('notifDetail.ip')}>{auditDetailRecord.ip || '-'}</Descriptions.Item>
              <Descriptions.Item label={t('common.details')}><span style={{ wordBreak: 'break-all' }}>{auditDetailRecord.details || '-'}</span></Descriptions.Item>
            </Descriptions>
          </div>
        )}
      </Modal>

      <Modal
        title={apiKeyEditing ? t('admin.editApiKey') : t('admin.createApiKey')}
        open={apiKeyModalOpen}
        onCancel={() => { setApiKeyModalOpen(false); setApiKeyEditing(null); apiKeyForm.resetFields() }}
        onOk={apiKeyEditing ? handleUpdateAPIKey : handleCreateAPIKey}
        okText={apiKeyEditing ? t('common.save') : t('admin.createApiKey')}
        cancelText={t('common.cancel')}
        width={{ sm: '90%', md: 600 }}
      >
        <Form form={apiKeyForm} layout="vertical">
          <Form.Item name="name" label={t('admin.name')} rules={[{ required: true, message: t('admin.nameRequired') }]}>
            <Input placeholder="My API Key" />
          </Form.Item>
          <Form.Item label={t('admin.permissions')}>
            <Space direction="vertical">
              <Form.Item name={['permissions', 'export_json']} valuePropName="checked" noStyle>
                <Checkbox>{t('admin.permExportJson')}</Checkbox>
              </Form.Item>
              <Form.Item name={['permissions', 'export_parsed']} valuePropName="checked" noStyle>
                <Checkbox>{t('admin.permExportParsed')}</Checkbox>
              </Form.Item>
              <Form.Item name={['permissions', 'view_stats']} valuePropName="checked" noStyle>
                <Checkbox>{t('admin.permViewStats')}</Checkbox>
              </Form.Item>
            </Space>
          </Form.Item>
          <Form.Item label={t('admin.scopeFilters')} tooltip={t('admin.scopeFiltersTooltip')}>
            <Space direction="vertical" style={{ width: '100%' }}>
              <Form.Item name={['scope_filters', 'hostnames']} label={t('admin.hostnames')} noStyle>
                <Select
                  mode="tags"
                  allowClear
                  style={{ width: '100%' }}
                  placeholder={t('admin.hostnamesPlaceholder')}
                  options={Array.from(new Set(devices.map(d => d.hostname).filter(Boolean))).map(h => ({ value: h, label: h }))}
                />
              </Form.Item>
              <Form.Item name={['scope_filters', 'severities']} label={t('admin.severities')} noStyle>
                <Select
                  mode="multiple"
                  allowClear
                  style={{ width: '100%' }}
                  placeholder={t('admin.severitiesPlaceholder')}
                  options={SEVERITY_ORDER.map(sev => ({ value: sev, label: getSeverityLabels(t)[sev] }))}
                  optionRender={(option) => <Tag color={SEVERITY_COLORS[option.value as string]}>{option.label}</Tag>}
                  tagRender={({ value, closable, onClose }) => (
                    <Tag color={SEVERITY_COLORS[value as string]} closable={closable} onClose={onClose} style={{ marginInlineEnd: 4 }}>
                      {getSeverityLabels(t)[value as string] || value}
                    </Tag>
                  )}
                />
              </Form.Item>
              <Form.Item name={['scope_filters', 'match_mode']} label={t('admin.scopeMatchMode')} tooltip={t('admin.scopeMatchModeTooltip')} initialValue="and" noStyle>
                <Select style={{ width: '100%' }}>
                  <Option value="and">{t('admin.scopeMatchModeAnd')}</Option>
                  <Option value="or">{t('admin.scopeMatchModeOr')}</Option>
                </Select>
              </Form.Item>
            </Space>
          </Form.Item>
          <Form.Item
            name="allowed_ips"
            label={t('admin.allowedIps')}
            tooltip={t('admin.allowedIpsTooltip')}
            rules={[{
              validator: (_, value: string[] = []) =>
                value.every(v => ipOrCidrPattern.test(v))
                  ? Promise.resolve()
                  : Promise.reject(new Error(t('admin.allowedIpsInvalid'))),
            }]}
          >
            <Select
              mode="tags"
              allowClear
              style={{ width: '100%' }}
              placeholder={t('admin.allowedIpsPlaceholder')}
              tokenSeparators={[',', ' ']}
              options={Array.from(new Set(devices.map(d => d.fromhost_ip).filter(Boolean))).map(ip => ({ value: ip, label: ip }))}
            />
          </Form.Item>
          <Form.Item name="rate_limit_per_min" label={t('admin.rateLimit')} rules={[{ required: true, message: t('admin.rateLimitRequired') }]}>
            <InputNumber min={1} max={10000} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="ttl_days" label={t('admin.ttlDays')}>
            <Select>
              <Select.Option value={7}>7 {t('admin.days')}</Select.Option>
              <Select.Option value={30}>30 {t('admin.days')}</Select.Option>
              <Select.Option value={90}>90 {t('admin.days')}</Select.Option>
              <Select.Option value={0}>{t('admin.never')}</Select.Option>
            </Select>
          </Form.Item>
          {!apiKeyEditing && (
            <Form.Item name="is_active" label={t('admin.active')} valuePropName="checked" initialValue={true}>
              <Switch />
            </Form.Item>
          )}
          {apiKeyEditing && (
            <Form.Item name="is_active" label={t('admin.active')} valuePropName="checked">
              <Switch />
            </Form.Item>
          )}
        </Form>
      </Modal>

      <Modal
        title={t('admin.apiKeyGenerated')}
        open={!!newKeyDisplay}
        onCancel={() => setNewKeyDisplay(null)}
        footer={
          <Button type="primary" block onClick={() => setNewKeyDisplay(null)}>
            {t('common.close')}
          </Button>
        }
        width={{ sm: '90%', md: 500 }}
      >
        <p>{t('admin.apiKeyCopyWarning')}</p>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <Input value={newKeyDisplay || ''} readOnly style={{ flex: 1 }} />
          <Button
            icon={<CopyOutlined />}
            onClick={() => {
              navigator.clipboard.writeText(newKeyDisplay || '')
              message.success(t('admin.apiKeyCopied'))
            }}
          >
            {t('admin.copy')}
          </Button>
        </div>
      </Modal>
    </div>
  )
}

// containerStateColor imported from ../utils/adminUtils

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
    case 'online': return i18nInstance.t('admin.relayOnline')
    case 'stale': return i18nInstance.t('admin.relayStale')
    case 'cert_revoked': return i18nInstance.t('admin.relayCertRevoked')
    default: return i18nInstance.t('admin.relayNeverSeen')
  }
}