import { useEffect, useRef, useState } from 'react'
import { Badge, Button, Card, Drawer, Dropdown, Empty, List, Space, Switch, Tag, Typography, message } from 'antd'
import { BellOutlined } from '@ant-design/icons'
import {
  getNotifications, markNotificationsRead, streamNotifications, InAppNotification,
  getVapidPublicKey, subscribePush, unsubscribePush,
} from '../services/api'
import { emitLiveNotification } from '../services/notificationEvents'
import { useIsMobile } from '../hooks/useIsMobile'
import { getErrorMessage } from '../utils/error'
import { SEVERITY_COLORS } from '../constants'

const severityColorMap: Record<string, string> = {
  critical: 'crit',
  error: 'err',
  warning: 'warning',
  info: 'info',
}

function getSeverityColor(severity: string) {
  return SEVERITY_COLORS[severityColorMap[severity] ?? severity] ?? 'default'
}

function formatTime(iso: string) {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  return d.toLocaleString()
}

const pushSupported = typeof window !== 'undefined' && 'serviceWorker' in navigator && 'PushManager' in window

function urlBase64ToUint8Array(base64String: string) {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4)
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/')
  const rawData = window.atob(base64)
  const outputArray = new Uint8Array(rawData.length)
  for (let i = 0; i < rawData.length; ++i) outputArray[i] = rawData.charCodeAt(i)
  return outputArray
}

// A stuck service worker, an unreachable VAPID endpoint, or a browser push
// service that never responds to subscribe() all resolve to the same silent
// failure mode: the enable-push promise chain just never settles, so the
// switch spins forever with no error. Race every step against a timeout so
// there's always a concrete answer instead of an indefinite hang.
function withTimeout<T>(promise: Promise<T>, ms: number, label: string): Promise<T> {
  return Promise.race([
    promise,
    new Promise<T>((_, reject) => setTimeout(() => reject(new Error(`Timed out ${label} (${ms / 1000}s)`)), ms)),
  ])
}

export function NotificationBell() {
  const isMobile = useIsMobile()
  const [enabled, setEnabled] = useState(false)
  const [items, setItems] = useState<InAppNotification[]>([])
  const [unreadCount, setUnreadCount] = useState(0)
  const [open, setOpen] = useState(false)
  const [pushSubscribed, setPushSubscribed] = useState(false)
  const [pushBusy, setPushBusy] = useState(false)
  const lastIdRef = useRef(0)

  useEffect(() => {
    let cancelled = false
    let stopStream: (() => void) | null = null

    getNotifications().then(res => {
      if (cancelled) return
      setEnabled(res.enabled !== false)
      setItems(res.notifications || [])
      setUnreadCount(res.unread_count || 0)
      lastIdRef.current = res.last_id || 0

      if (res.enabled !== false) {
        stopStream = streamNotifications((n) => {
          setItems(prev => [n, ...prev].slice(0, 50))
          setUnreadCount(prev => prev + 1)
          emitLiveNotification(n)
        })
      }
    }).catch(() => { /* ignore */ })

    return () => {
      cancelled = true
      stopStream?.()
    }
  }, [])

  useEffect(() => {
    if (!enabled || !pushSupported) return
    let cancelled = false
    navigator.serviceWorker.ready
      .then(reg => reg.pushManager.getSubscription())
      .then(sub => { if (!cancelled) setPushSubscribed(!!sub) })
      .catch(() => { /* ignore */ })
    return () => { cancelled = true }
  }, [enabled])

  if (!enabled) return null

  const handleTogglePush = async (checked: boolean) => {
    setPushBusy(true)
    try {
      if (checked) {
        // Request permission first, as the very first await in this
        // handler - it needs to run on the click's user-activation, and
        // waiting on anything else (like serviceWorker.ready) beforehand
        // risks losing that on stricter mobile browsers, or hanging here
        // indefinitely if the service worker is stuck installing.
        const permission = await Notification.requestPermission()
        if (permission !== 'granted') {
          message.warning('Notification permission was not granted')
          return
        }
        // Register explicitly here rather than relying on the fire-and-
        // forget call in main.tsx: that one only console.warns on failure,
        // which nobody sees on a phone - if registration itself is broken
        // (insecure origin, script blocked, wrong MIME type), this makes
        // that error land in the toast instead of a 10s "waiting" timeout
        // for a worker that was never going to show up.
        let reg = await navigator.serviceWorker.getRegistration()
        if (!reg) {
          reg = await withTimeout(navigator.serviceWorker.register('/sw.js'), 10_000, 'registering the service worker')
        }
        reg = await withTimeout(navigator.serviceWorker.ready, 10_000, 'waiting for the service worker')
        const publicKey = await withTimeout(getVapidPublicKey(), 10_000, 'fetching the VAPID key from the server')
        const sub = await withTimeout(
          reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToUint8Array(publicKey) }),
          15_000, 'subscribing with the browser push service',
        )
        await withTimeout(subscribePush(sub.toJSON()), 10_000, 'saving the subscription on the server')
        setPushSubscribed(true)
        message.success('Push notifications enabled')
      } else {
        const reg = await withTimeout(navigator.serviceWorker.ready, 10_000, 'waiting for the service worker')
        const sub = await reg.pushManager.getSubscription()
        if (sub) {
          await unsubscribePush(sub.endpoint)
          await sub.unsubscribe()
        }
        setPushSubscribed(false)
        message.success('Push notifications disabled')
      }
    } catch (e) {
      console.error('push notification toggle failed', e)
      message.error(getErrorMessage(e, 'Failed to update push notification setting'))
    } finally {
      setPushBusy(false)
    }
  }

  const handleOpen = () => {
    setOpen(true)
    if (unreadCount > 0) {
      const maxID = items.reduce((max, n) => Math.max(max, n.id), lastIdRef.current)
      lastIdRef.current = maxID
      markNotificationsRead(maxID).catch(() => { /* ignore */ })
      setUnreadCount(0)
    }
  }

  const handleClearAll = () => {
    markNotificationsRead(lastIdRef.current).catch(() => { /* ignore */ })
    setItems([])
    setUnreadCount(0)
  }

  // Jumps to the alert History tab and opens the same "Details" view this
  // notification's dispatch produced there - see HistoryTab's focus-by-id
  // handling in Alerts.tsx, matched via in_app_notification_id. A full
  // navigation (not react-router) matches how other cross-page links in
  // this app already work (e.g. device name -> filtered Logs view).
  const goToHistoryDetail = (notificationId: number) => {
    setOpen(false)
    window.location.href = `/alerts?tab=history&notification=${notificationId}`
  }

  const panelBody = items.length === 0 ? (
    <Empty description="No notifications" style={{ padding: 24 }} />
  ) : (
    <List
      dataSource={items}
      renderItem={(n) => (
        <List.Item
          style={{ padding: '10px 16px', display: 'block', cursor: 'pointer' }}
          onClick={() => goToHistoryDetail(n.id)}
        >
          <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
            <Typography.Text strong style={{ fontSize: 13 }}>{n.title}</Typography.Text>
            <Tag color={getSeverityColor(n.severity)} style={{ marginRight: 0 }}>{n.severity}</Tag>
          </div>
          <Typography.Text type="secondary" style={{ fontSize: 13, display: 'block', marginTop: 2 }}>{n.message}</Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 11, display: 'block', marginTop: 4 }}>{formatTime(n.created_at)}</Typography.Text>
        </List.Item>
      )}
    />
  )

  const clearButton = items.length > 0 && (
    <Button type="link" size="small" style={{ padding: 0 }} onClick={handleClearAll}>Clear all</Button>
  )

  const pushRow = pushSupported && (
    <div style={{ padding: '8px 16px', display: 'flex', alignItems: 'center', justifyContent: 'space-between', borderBottom: '1px solid rgba(128,128,128,0.2)' }}>
      <Typography.Text style={{ fontSize: 12 }}>Push notifications</Typography.Text>
      <Switch size="small" checked={pushSubscribed} loading={pushBusy} onChange={handleTogglePush} />
    </div>
  )

  const panelContent = (
    <>
      {pushRow}
      {panelBody}
    </>
  )

  const bellButton = (
    <Button
      type="text"
      onClick={isMobile ? handleOpen : undefined}
      icon={
        <Badge count={unreadCount} size="small" offset={[-2, 2]}>
          <BellOutlined style={{ fontSize: 18 }} />
        </Badge>
      }
    />
  )

  if (isMobile) {
    return (
      <>
        {bellButton}
        <Drawer
          title="Notifications"
          placement="bottom"
          height="70%"
          open={open}
          onClose={() => setOpen(false)}
          extra={clearButton}
          styles={{ body: { padding: 0, overflowY: 'auto' } }}
        >
          {panelContent}
        </Drawer>
      </>
    )
  }

  return (
    <Dropdown
      open={open}
      onOpenChange={(next) => { if (next) handleOpen(); else setOpen(false) }}
      trigger={['click']}
      placement="bottomRight"
      dropdownRender={() => (
        <Card
          size="small"
          title="Notifications"
          extra={clearButton}
          style={{ width: 360 }}
          styles={{ body: { padding: 0, maxHeight: 380, overflowY: 'auto' } }}
        >
          {panelContent}
        </Card>
      )}
    >
      {bellButton}
    </Dropdown>
  )
}
