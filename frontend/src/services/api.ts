import axios from 'axios'

export const api = axios.create({
  baseURL: '/api',
  timeout: 30000,
  withCredentials: true,
})

function getCookie(name: string): string | undefined {
  const match = document.cookie.match(new RegExp('(^| )' + name + '=([^;]+)'))
  return match ? decodeURIComponent(match[2]) : undefined
}

api.interceptors.request.use(config => {
  if (config.method && config.method !== 'get' && config.method !== 'head' && config.method !== 'options') {
    const csrfToken = getCookie('csrf_token')
    if (csrfToken) {
      config.headers['X-CSRF-Token'] = csrfToken
    }
  }
  return config
})

let isRefreshing = false
let retryQueue: Array<{ resolve: (value: unknown) => void; reject: () => void }> = []

const processQueue = (error: unknown | null) => {
  retryQueue.forEach(cb => {
    if (error) cb.reject()
    else cb.resolve(null)
  })
  retryQueue = []
}

api.interceptors.response.use(
  response => response,
  async error => {
    const originalRequest = error.config
    if (error.response?.status === 401 && !originalRequest._retry && originalRequest.url !== '/auth/login' && originalRequest.url !== '/auth/refresh' && window.location.pathname !== '/login') {
      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          retryQueue.push({ resolve, reject })
        }).then(() => api(originalRequest))
      }
      originalRequest._retry = true
      isRefreshing = true
      try {
        const res = await api.post('/auth/refresh')
        processQueue(null)
        return api(originalRequest)
      } catch (err) {
        processQueue(err)
        window.location.href = '/login'
        return Promise.reject(err)
      } finally {
        isRefreshing = false
      }
    }
    return Promise.reject(error)
  }
)

export interface LogEntry {
  id: number
  timestamp: string
  hostname: string
  fromhost_ip?: string
  app_name?: string
  process_id?: string
  msg_id?: string
  severity: string
  facility?: string
  message: string
  raw_message?: string
  parsed_fields?: Record<string, string>
  matched_parsers?: string[]
  created_at: string
  display_name?: string
}

export interface DashboardStats {
  total_logs: number
  logs_last_hour: number
  logs_last_day: number
  unique_devices: number
  severity_counts: Record<string, number>
  top_devices: Array<{ hostname: string; count: number }>
  top_errors: Array<{ message: string; count: number; hostname: string }>
}

export interface TimelinePoint {
  timestamp: string
  count: number
}

export interface DeviceStats {
  fromhost_ip: string
  hostname: string
  old_hostname?: string
  display_name?: string
  total_logs: number
  last_seen: string
  severity_count: Record<string, number>
  matched_parsers: string[]
  has_parsed: boolean
}

export interface LogsPage {
  logs: LogEntry[]
  has_more: boolean
  next_cursor: string
  limit: number
}

// Sorts that support cheap keyset pagination via `cursor`. Deep pagination
// on the other sorts (severity/hostname) falls back to `offset`, which is
// fine since those are rarely paged deeply.
export function sortSupportsCursor(sort: string): boolean {
  return sort === '' || sort === 'timestamp_desc' || sort === 'timestamp_asc'
}

export async function getLogs(params: {
  offset?: number
  cursor?: string
  limit?: number
  hostname?: string
  fromhost_ip?: string
  severity?: string
  app_name?: string
  search?: string
  from?: string
  to?: string
  sort?: string
}): Promise<LogsPage> {
  const res = await api.post('/logs', params)
  return res.data || { logs: [], has_more: false, next_cursor: '', limit: params.limit || 50 }
}

export async function getLogsCount(params: {
  hostname?: string
  fromhost_ip?: string
  severity?: string
  app_name?: string
  search?: string
  from?: string
  to?: string
}): Promise<number> {
  const res = await api.post('/logs/count', params)
  return res.data?.total || 0
}

export async function getDashboardStats(): Promise<DashboardStats> {
  const res = await api.get('/stats/dashboard')
  return (res.data || {}) as DashboardStats
}

export async function getTimeline(interval = '1h', from?: string, to?: string) {
  const res = await api.get('/stats/timeline', { params: { interval, from, to } })
  return (res.data?.timeline || []) as TimelinePoint[]
}

export async function getDevices() {
  const res = await api.get('/devices')
  return (res.data?.devices || []) as DeviceStats[]
}

export function resolveDeviceDisplayName(device: DeviceStats): string {
  return device.display_name || device.hostname || device.fromhost_ip || '-'
}

export async function getDeviceStats() {
  const res = await api.get('/devices')
  return (res.data?.devices || []) as DeviceStats[]
}

export async function updateDeviceAlias(ip: string, displayName: string) {
  const res = await api.put(`/admin/devices/${ip}/alias`, { display_name: displayName })
  return res.data
}

export async function getSeverityStats(from?: string, to?: string) {
  const res = await api.get('/stats/severity', { params: { from, to } })
  return (res.data?.stats || []) as Array<{ severity: string; count: number }>
}

export async function exportCSV(params: Record<string, string>) {
  const res = await api.get('/export/csv', { params, responseType: 'blob' })
  const url = URL.createObjectURL(res.data)
  const a = document.createElement('a')
  a.href = url
  a.download = 'syslog_export.csv'
  a.click()
  URL.revokeObjectURL(url)
}

export async function exportHTML(params: Record<string, string>) {
  const res = await api.get('/export/html', { params, responseType: 'blob' })
  const url = URL.createObjectURL(res.data)
  const a = document.createElement('a')
  a.href = url
  a.download = 'syslog_report.html'
  a.click()
  URL.revokeObjectURL(url)
}

export async function exportDashboardCSV(id: number, params: Record<string, string>) {
  const res = await api.get(`/dashboards/${id}/export/csv`, { params, responseType: 'blob' })
  const url = URL.createObjectURL(res.data)
  const a = document.createElement('a')
  a.href = url
  a.download = 'syslog_export.csv'
  a.click()
  URL.revokeObjectURL(url)
}

export async function exportDashboardHTML(id: number, params: Record<string, string>) {
  const res = await api.get(`/dashboards/${id}/export/html`, { params, responseType: 'blob' })
  const url = URL.createObjectURL(res.data)
  const a = document.createElement('a')
  a.href = url
  a.download = 'syslog_report.html'
  a.click()
  URL.revokeObjectURL(url)
}

// --- Parser types ---
export interface Parser {
  id: number
  name: string
  description: string | null
  device_type: string
  match_type: string
  match_value: string | null
  regex: string
  enabled: boolean
  is_builtin: boolean
  created_at: string
  updated_at: string
}

export interface ParsedField {
  parser_id: number
  parser_name: string
  field_name: string
  field_label: string
  field_type: string
}

export interface ParserFieldDef {
  name: string
  label: string
  type: string
}

export interface ParserTestResponse {
  matched: boolean
  parser_name: string | null
  fields: Record<string, string> | null
  message: string
}

// --- Dashboard types ---
export interface DashboardFilters {
  severity: string
  from: string
  to: string
  search: string
}

export interface DashboardConfig {
  devices: string[]
  parsers?: string[]
  fields: string[]
  filters: DashboardFilters
}

export interface Dashboard {
	id: number
	name: string
	description: string | null
	owner_id: number
	owner_username: string
	pinned: boolean
	is_public: boolean
	config: DashboardConfig
	created_at: string
	updated_at: string
	updated_by_user_id: number
	updated_by_username: string
}

export interface DashboardDataResponse {
  logs: LogEntry[]
  has_more: boolean
  next_cursor: string
  fields: string[]
  devices: string[]
}

// --- Parser API ---
export async function getParsers() {
  const res = await api.get('/parsers')
  return (res.data || []) as Parser[]
}

export async function createParser(data: {
  name: string
  description?: string
  device_type: string
  match_type: string
  match_value?: string
  regex: string
  enabled: boolean
  fields: ParserFieldDef[]
}) {
  const res = await api.post('/parsers', data)
  return res.data
}

export async function updateParser(id: number, data: Partial<{
  name: string
  description: string
  device_type: string
  match_type: string
  match_value: string
  regex: string
  enabled: boolean
}>) {
  const res = await api.put(`/parsers/${id}`, data)
  return res.data
}

export async function deleteParser(id: number) {
  const res = await api.delete(`/parsers/${id}`)
  return res.data
}

export async function cloneParser(id: number) {
  const res = await api.post(`/parsers/${id}/clone`)
  return res.data
}

export async function testParser(pattern: string, sampleLog: string): Promise<ParserTestResponse> {
  const res = await api.post('/parsers/test', { pattern, sample_log: sampleLog })
  return res.data as ParserTestResponse
}

export async function reparseUnparsed(hostname?: string, from?: string, to?: string, limit?: number) {
  const res = await api.post('/parsers/reparse', { hostname, from, to, limit })
  return res.data
}

export async function getParsedFields(hostnames?: string[]) {
  const params: Record<string, string> = {}
  if (hostnames && hostnames.length > 0) {
    params.hostnames = hostnames.join(',')
  }
  const res = await api.get('/parsers/fields', { params })
  return (res.data || []) as ParsedField[]
}

// --- Dashboard API ---
export async function getDashboards() {
  const res = await api.get('/dashboards')
  return (res.data || []) as Dashboard[]
}

export async function createDashboard(data: {
  name: string
  description?: string
  config: DashboardConfig
}) {
  const res = await api.post('/dashboards', data)
  return res.data
}

export async function getDashboard(id: number) {
  const res = await api.get(`/dashboards/${id}`)
  return res.data as Dashboard
}

export async function updateDashboard(id: number, data: Partial<{
  name: string
  description: string
  config: DashboardConfig
}>) {
  const res = await api.put(`/dashboards/${id}`, data)
  return res.data
}

export async function deleteDashboard(id: number) {
  const res = await api.delete(`/dashboards/${id}`)
  return res.data
}

export async function getDashboardData(id: number, limit = 100, cursor = '', search = '', severity = '', from = '', to = '', sort = 'timestamp_desc', offset = 0, fromHostIp = '') {
	const res = await api.get(`/dashboards/${id}/data`, { params: { limit, cursor: cursor || undefined, search: search || undefined, severity: severity || undefined, from: from || undefined, to: to || undefined, sort: sort || undefined, offset: (!sortSupportsCursor(sort) && offset) || undefined, fromhost_ip: fromHostIp || undefined } })
	const d = res.data || {}
	return { logs: d.logs || [], has_more: d.has_more || false, next_cursor: d.next_cursor || '', fields: d.fields || [], devices: d.devices || [] } as DashboardDataResponse
}

export async function getDashboardDataCount(id: number, search = '', severity = '', from = '', to = '', fromHostIp = ''): Promise<number> {
	const res = await api.get(`/dashboards/${id}/count`, { params: { search: search || undefined, severity: severity || undefined, from: from || undefined, to: to || undefined, fromhost_ip: fromHostIp || undefined } })
	return res.data?.total || 0
}

export async function togglePinDashboard(id: number) {
	const res = await api.patch(`/dashboards/${id}/pin`)
	return res.data as { pinned: boolean }
}

export async function togglePublicDashboard(id: number) {
	const res = await api.patch(`/dashboards/${id}/public`)
	return res.data as { is_public: boolean }
}

// --- User / Admin types ---
export interface User {
	id: number
	username: string
	email: string
	role: string
	auth_type: string
	is_admin: boolean
	is_active: boolean
	created_at: string
	last_login_at: string | null
	failed_login_attempts: number
	locked_until: string | null
}

export async function getUsers() {
	const res = await api.get('/admin/users')
	return (res.data || []) as User[]
}

export async function createUser(data: { username: string; email: string; password: string; role: string; auth_type: string }) {
	const res = await api.post('/admin/users', data)
	return res.data as User
}

export async function updateUser(id: number, data: { role?: string; is_active?: boolean }) {
	const res = await api.put(`/admin/users/${id}`, data)
	return res.data as User
}

export async function deleteUser(id: number) {
	const res = await api.delete(`/admin/users/${id}`)
	return res.data
}

export async function resetPassword(id: number, password: string) {
	const res = await api.put(`/admin/users/${id}/reset-password`, { password })
	return res.data
}

export async function unlockUser(id: number) {
	const res = await api.post(`/admin/users/${id}/unlock`)
	return res.data
}

export async function getSettings() {
	const res = await api.get('/admin/settings')
	return res.data as Record<string, string>
}

export async function updateSettings(settings: Record<string, string>) {
	const res = await api.put('/admin/settings', settings)
	return res.data
}

export async function cleanupLogs() {
	const res = await api.post('/admin/settings/cleanup')
	return res.data
}

export async function purgeAllLogs(pauseDuringPurge: boolean) {
	const res = await api.delete('/admin/logs', { data: { pause_during_purge: pauseDuringPurge } })
	return res.data
}

export async function pauseIngestion() {
	const res = await api.post('/admin/ingestion/pause')
	return res.data
}

export async function resumeIngestion() {
	const res = await api.post('/admin/ingestion/resume')
	return res.data
}

export async function testLDAPConnection(data: {
	server: string
	port: number
	use_tls: boolean
	verify_cert: boolean
	ca_cert: string
	base_dn: string
	bind_dn: string
	bind_password: string
}) {
	const res = await api.post('/admin/ldap/test', data)
	return res.data
}

// --- Init / Setup ---
export async function checkInitialized() {
	const res = await api.get('/status/initialized')
	return res.data as { initialized: boolean; starting: boolean }
}

export async function generateKeys() {
	const res = await api.get('/init/generate-keys')
	return res.data as { jwt_secret: string; encryption_key: string }
}

export interface InitRequest {
	admin: {
		username: string
		email: string
		password: string
	}
	database: {
		host: string
		port: number
		name: string
		user: string
		password: string
	}
	jwt_secret: string
	encryption_key: string
	cors_origins?: string
	ldap?: {
		server: string
		port: number
		use_tls: boolean
		verify_cert: boolean
		ca_cert: string
		base_dn: string
		bind_dn: string
		bind_password: string
	}
}

export async function initialize(data: InitRequest) {
	const res = await api.post('/init', data)
	return res.data
}

export interface DbConfig {
	configured: boolean
	host: string
	port: number
	name: string
	user: string
	password: string
}

export async function getDbConfig() {
	const res = await api.get('/init/db-config')
	return res.data as DbConfig
}

export interface DatabaseSettings {
	host: string
	port: number
	name: string
	user: string
	password: string
}

export async function testDbConfig(data: DatabaseSettings) {
	const res = await api.post('/init/test-db', data)
	return res.data as { message: string }
}

export interface SlowQueryRecord {
	name: string
	duration_ms: number
	timestamp: string
}

export async function getSlowQueries() {
	const res = await api.get('/admin/slow-queries')
	return res.data as SlowQueryRecord[]
}

export async function clearSlowQueries() {
	const res = await api.delete('/admin/slow-queries')
	return res.data
}

// --- Health (Admin > Health) ---

export interface ContainerHealth {
	name: string
	image: string
	state: string
	status: string
	health?: string
	node?: string
}

export interface ServiceHealth {
	name: string
	mode: string
	image: string
	replicas_desired: number
	replicas_running: number
	tasks: ContainerHealth[]
}

export interface RelayHealth {
	label: string
	ip_address: string
	cert_status: string
	last_seen?: string
	seconds_since_seen?: number
	status: 'online' | 'stale' | 'never_seen' | 'cert_revoked'
}

export interface ContainersHealthResponse {
	docker_available: boolean
	mode: 'single' | 'swarm' | ''
	scope: 'cluster' | 'node' | ''
	containers?: ContainerHealth[]
	services?: ServiceHealth[]
	relays: RelayHealth[]
	message?: string
	refreshed_at: string
}

export async function getContainersHealth() {
	const res = await api.get('/admin/health/containers')
	return res.data as ContainersHealthResponse
}

export async function uploadSSLCerts(certFile: File, keyFile: File) {
	const formData = new FormData()
	formData.append('cert', certFile)
	formData.append('key', keyFile)
	const res = await api.post('/admin/ssl/upload', formData, {
		headers: { 'Content-Type': 'multipart/form-data' },
		timeout: 60000,
	})
	return res.data
}

// --- Syslog Relay (Admin > Syslog Relay) ---

export interface RelayWhitelistEntry {
	id: number
	ip_address: string
	label: string
	relay_cert_id?: number
	created_at: string
	created_by?: number
}

export async function getRelayWhitelist() {
	const res = await api.get('/admin/relay/whitelist')
	return (res.data || []) as RelayWhitelistEntry[]
}

export async function addRelayWhitelistEntry(data: { ip_address: string; label: string }) {
	const res = await api.post('/admin/relay/whitelist', data)
	return res.data
}

export async function deleteRelayWhitelistEntry(id: number) {
	const res = await api.delete(`/admin/relay/whitelist/${id}`)
	return res.data
}

export interface RelayCertificate {
	id: number
	label: string
	serial_hex: string
	fingerprint_sha256: string
	status: 'issued' | 'revoked'
	issued_at: string
	expires_at: string
	issued_by?: number
	revoked_at?: string | null
}

// Mirrors backend model.RelayCertRenewalWindowDays - how close to its own
// expiry an "issued" certificate must be before the backend will allow
// regenerateRelayCertificate to renew it.
export const RELAY_CERT_RENEWAL_WINDOW_DAYS = 30

export async function getRelayCertificates() {
	const res = await api.get('/admin/relay/certificates')
	return (res.data || []) as RelayCertificate[]
}

// responseType: 'blob' means an error response's JSON body arrives as a
// Blob instead of being parsed - read it back out so getErrorMessage can
// still surface the real "IP already whitelisted"-style message instead of
// a generic fallback.
async function normalizeBlobError(e: unknown): Promise<Error> {
	if (e && typeof e === 'object' && 'response' in e) {
		const resp = (e as { response?: { data?: unknown } }).response
		if (resp?.data instanceof Blob) {
			try {
				const text = await resp.data.text()
				const parsed = JSON.parse(text)
				if (parsed?.error) return new Error(parsed.error)
			} catch {
				// not JSON - fall through to the generic error below
			}
		}
	}
	return e instanceof Error ? e : new Error('Request failed')
}

// downloadCertificateBundle POSTs to url and triggers a one-time browser
// download of the resulting .tar.gz - shared by both ways of issuing a
// relay certificate below. The API never lets the bundle be fetched again
// after this call, so the caller must warn the user to save it now.
async function downloadCertificateBundle(url: string, body: unknown, fallbackFilename: string) {
	let res
	try {
		res = await api.post(url, body, { responseType: 'blob' })
	} catch (e: unknown) {
		throw await normalizeBlobError(e)
	}
	const disposition = res.headers['content-disposition'] as string | undefined
	const match = disposition?.match(/filename="?([^"]+)"?/)
	const filename = match ? match[1] : fallbackFilename
	const downloadUrl = URL.createObjectURL(res.data)
	const a = document.createElement('a')
	a.href = downloadUrl
	a.download = filename
	a.click()
	URL.revokeObjectURL(downloadUrl)
	return { filename }
}

// Issues a new relay certificate and whitelists its IP in the same step.
// Fails if the IP is already whitelisted - use
// generateRelayCertificateForWhitelistEntry for that case instead.
export async function generateRelayCertificate(data: { label: string; ip_address: string }) {
	return downloadCertificateBundle('/admin/relay/certificates', data, `syslog-relay-${data.label}.tar.gz`)
}

// Issues a certificate for an IP that's already whitelisted (added via
// "Add IP" without one, or whose previous certificate was revoked) and
// links it to that entry.
export async function generateRelayCertificateForWhitelistEntry(id: number, label: string) {
	return downloadCertificateBundle(`/admin/relay/whitelist/${id}/certificate`, undefined, `syslog-relay-${label}.tar.gz`)
}

// Reissues a certificate for a revoked one's whitelist entry (found
// server-side by reverse lookup) - the old, revoked row stays in the list
// for the audit trail.
export async function regenerateRelayCertificate(id: number, label: string) {
	return downloadCertificateBundle(`/admin/relay/certificates/${id}/regenerate`, undefined, `syslog-relay-${label}.tar.gz`)
}

export async function revokeRelayCertificate(id: number) {
	const res = await api.delete(`/admin/relay/certificates/${id}`)
	return res.data
}

// --- Alerts & Notifications ---

export type AlertRuleType = 'log_threshold' | 'device_silence' | 'config_change' | 'relay_cert_expiring'
export type NotificationChannelType = 'email' | 'webhook' | 'slack' | 'teams' | 'in_app' | 'push'
export type FieldConditionOperator = 'equals' | 'contains' | 'not_equals' | 'regex'

export type FieldConditionsLogic = 'and' | 'or'

export interface AlertFieldCondition {
	id?: number
	field_name: string
	operator: FieldConditionOperator
	value: string
}

export interface Alert {
	id: number
	name: string
	description: string
	rule_type: AlertRuleType
	severity?: string
	device_ips: string[]
	parser_names: string[]
	field_conditions: AlertFieldCondition[]
	field_conditions_logic: FieldConditionsLogic
	message_pattern?: string
	threshold: number
	window_minutes: number
	cooldown_minutes: number
	fire_on_every_match: boolean
	audit_action_filter?: string
	is_active: boolean
	created_by?: number
	created_at: string
	updated_at: string
	last_fired_at?: string
	channel_ids: number[]
}

export interface AlertRequest {
	name: string
	description?: string
	rule_type: AlertRuleType
	severity?: string
	device_ips?: string[]
	parser_names?: string[]
	field_conditions?: AlertFieldCondition[]
	field_conditions_logic?: FieldConditionsLogic
	message_pattern?: string
	threshold?: number
	window_minutes?: number
	cooldown_minutes?: number
	fire_on_every_match?: boolean
	audit_action_filter?: string
	is_active?: boolean
	channel_ids?: number[]
}

export async function getAlerts() {
	const res = await api.get('/alerts')
	return (res.data || []) as Alert[]
}

export async function createAlert(data: AlertRequest) {
	const res = await api.post('/alerts', data)
	return res.data as Alert
}

export async function updateAlert(id: number, data: AlertRequest) {
	const res = await api.put(`/alerts/${id}`, data)
	return res.data as Alert
}

export async function deleteAlert(id: number) {
	const res = await api.delete(`/alerts/${id}`)
	return res.data
}

export interface NotificationChannel {
	id: number
	name: string
	type: NotificationChannelType
	config: Record<string, unknown>
	has_secret: boolean
	enabled: boolean
	created_at: string
	updated_at: string
}

export interface NotificationChannelRequest {
	name: string
	type: NotificationChannelType
	config?: Record<string, unknown>
	secret?: string
	enabled?: boolean
}

export async function getNotificationChannels() {
	const res = await api.get('/admin/notification-channels')
	return (res.data || []) as NotificationChannel[]
}

export async function createNotificationChannel(data: NotificationChannelRequest) {
	const res = await api.post('/admin/notification-channels', data)
	return res.data as NotificationChannel
}

export async function updateNotificationChannel(id: number, data: NotificationChannelRequest) {
	const res = await api.put(`/admin/notification-channels/${id}`, data)
	return res.data as NotificationChannel
}

export async function deleteNotificationChannel(id: number) {
	const res = await api.delete(`/admin/notification-channels/${id}`)
	return res.data
}

export async function testNotificationChannel(id: number) {
	const res = await api.post(`/admin/notification-channels/${id}/test`)
	return res.data
}

export interface TriggerLogSnapshot {
	timestamp: string
	hostname: string
	fromhost_ip: string
	app_name?: string
	severity: string
	message: string
}

export interface NotificationLogEntry {
	id: number
	alert_id?: number
	alert_name: string
	firing_id?: string
	channel_id?: number
	channel_name: string
	channel_type: string
	status: 'sent' | 'partial' | 'failed' | 'no_channel'
	detail?: string
	trigger_log?: TriggerLogSnapshot
	in_app_notification_id?: number
	matched_conditions?: string[]
	created_at: string
}

export async function getNotificationHistory(limit = 100) {
	const res = await api.get('/admin/notifications/history', { params: { limit } })
	return (res.data || []) as NotificationLogEntry[]
}

export async function clearNotificationHistory() {
	const res = await api.delete('/admin/notifications/history')
	return res.data
}

export interface InAppNotification {
	id: number
	alert_id?: number
	title: string
	message: string
	severity: string
	created_at: string
}

export async function getNotifications() {
	const res = await api.get('/notifications')
	return res.data as { enabled: boolean; unread_count: number; last_id: number; notifications: InAppNotification[] }
}

export async function markNotificationsRead(lastReadId: number) {
	const res = await api.post('/notifications/mark-read', { last_read_id: lastReadId })
	return res.data
}

// --- Web Push ---

export async function getVapidPublicKey() {
	const res = await api.get('/push/vapid-public-key')
	return (res.data as { public_key: string }).public_key
}

export async function subscribePush(subscription: PushSubscriptionJSON) {
	const res = await api.post('/push/subscribe', subscription)
	return res.data
}

export async function unsubscribePush(endpoint: string) {
	const res = await api.post('/push/unsubscribe', { endpoint })
	return res.data
}

// Opens the SSE notification stream via fetch (not EventSource) so the JWT
// can travel in the Authorization header instead of a URL query param.
// Calls onNotification for each event and returns a function that aborts
// the stream. Silently stops retrying once `stop()` has been called.
export function streamNotifications(onNotification: (n: InAppNotification) => void): () => void {
	const controller = new AbortController()
	let stopped = false

	const connect = async () => {
		while (!stopped) {
			try {
				const res = await fetch('/api/notifications/stream', {
					credentials: 'include',
					signal: controller.signal,
				})
				if (res.status === 401 || res.status === 403) {
					window.location.href = '/login'
					return
				}
				if (!res.ok || !res.body) throw new Error(`stream failed: ${res.status}`)

				const reader = res.body.getReader()
				const decoder = new TextDecoder()
				let buffer = ''

				while (!stopped) {
					const { done, value } = await reader.read()
					if (done) break
					buffer += decoder.decode(value, { stream: true })

					let sepIndex
					while ((sepIndex = buffer.indexOf('\n\n')) !== -1) {
						const rawEvent = buffer.slice(0, sepIndex)
						buffer = buffer.slice(sepIndex + 2)
						const dataLine = rawEvent.split('\n').find(l => l.startsWith('data: '))
						if (!dataLine) continue
						try {
							onNotification(JSON.parse(dataLine.slice(6)) as InAppNotification)
						} catch {
							// ignore malformed event
						}
					}
				}
			} catch {
				if (stopped) return
			}
			if (!stopped) await new Promise(r => setTimeout(r, 3000))
		}
	}

	connect()
	return () => {
		stopped = true
		controller.abort()
	}
}