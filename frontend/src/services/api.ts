import axios from 'axios'

export const api = axios.create({
  baseURL: '/api',
  timeout: 30000,
})

api.interceptors.request.use(config => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
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
    if (error.response?.status === 401 && !originalRequest._retry && originalRequest.url !== '/auth/login' && originalRequest.url !== '/auth/refresh') {
      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          retryQueue.push({ resolve, reject })
        }).then(() => api(originalRequest))
      }
      originalRequest._retry = true
      isRefreshing = true
      try {
        const refreshToken = localStorage.getItem('refresh_token')
        if (!refreshToken) throw new Error('No refresh token')
        const res = await api.post('/auth/refresh', { refresh_token: refreshToken })
        const newToken = res.data.token
        const newRT = res.data.refresh_token
        localStorage.setItem('token', newToken)
        localStorage.setItem('refresh_token', newRT)
        api.defaults.headers.common['Authorization'] = `Bearer ${newToken}`
        originalRequest.headers.Authorization = `Bearer ${newToken}`
        processQueue(null)
        return api(originalRequest)
      } catch (err) {
        processQueue(err)
        localStorage.removeItem('token')
        localStorage.removeItem('refresh_token')
        window.location.href = '/login'
        return Promise.reject(err)
      } finally {
        isRefreshing = false
      }
    }
    return Promise.reject(error)
  }
)

export async function refreshAccessToken(refreshToken: string) {
  const res = await api.post('/auth/refresh', { refresh_token: refreshToken })
  return res.data
}

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