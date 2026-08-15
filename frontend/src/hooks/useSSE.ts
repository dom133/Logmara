import { useEffect, useRef, useState, useCallback } from 'react'
import { LogEntry } from '../services/api'

interface UseSSEOptions {
  onNewLogs: (logs: LogEntry[]) => void
  filters?: {
    hostname?: string
    fromhost_ip?: string
    severity?: string
    search?: string
    from?: string
    to?: string
    require_parser?: string
  }
  enabled?: boolean
}

function parseSSE(data: string): Array<{ event?: string; data?: string }> {
  const events: Array<{ event?: string; data?: string }> = []
  let currentEvent = ''
  let currentData = ''

  for (const line of data.split('\n')) {
    if (line.startsWith('event:')) {
      currentEvent = line.slice(6).trim()
    } else if (line.startsWith('data:')) {
      currentData = line.slice(5).trim()
    } else if (line === '') {
      if (currentEvent || currentData) {
        events.push({ event: currentEvent, data: currentData })
        currentEvent = ''
        currentData = ''
      }
    }
  }

  if (currentEvent || currentData) {
    events.push({ event: currentEvent, data: currentData })
  }

  return events
}

export function useSSE({ onNewLogs, filters = {}, enabled = true }: UseSSEOptions) {
  const [connected, setConnected] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  const onNewLogsRef = useRef(onNewLogs)
  const filtersRef = useRef(filters)
  const enabledRef = useRef(enabled)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const isTabActiveRef = useRef(true)
  const eventSourceRef = useRef<EventSource | null>(null) // Add EventSource reference
  onNewLogsRef.current = onNewLogs
  filtersRef.current = filters
  enabledRef.current = enabled

  const connect = useCallback(() => {
    // Clean up existing connection
    if (eventSourceRef.current) {
      try {
        eventSourceRef.current.close()
      } catch (e) {
        console.warn('Error closing previous EventSource:', e)
      }
      eventSourceRef.current = null
    }

    // Check if tab is still active
    if (!isTabActiveRef.current) {
      setConnected(false)
      return
    }

    const controller = new AbortController()
    abortRef.current = controller

    const url = new URL('/api/logs/stream', window.location.href)
    Object.entries(filtersRef.current).forEach(([key, value]) => {
      if (value) {
        url.searchParams.set(key, value)
      }
    })

    const token = localStorage.getItem('token')
    if (!token) {
      setConnected(false)
      return
    }

    // Create EventSource properly for streaming
    try {
      const eventSource = new EventSource(url.toString() + `&token=${encodeURIComponent(token)}`)
      eventSourceRef.current = eventSource

      eventSource.onmessage = (event) => {
        if (event.data) {
          try {
            const decoded = atob(event.data)
            const logs = JSON.parse(decoded) as LogEntry[]
            if (Array.isArray(logs) && logs.length > 0) {
              onNewLogsRef.current(logs)
            }
          } catch (e) {
            // ignore parse errors
          }
        }
      }

      eventSource.onerror = (error) => {
        console.error('SSE stream error:', error)
        setConnected(false)
        eventSource.close()
        eventSourceRef.current = null
        scheduleReconnect()
      }

      eventSource.onopen = () => {
        setConnected(true)
        console.log('SSE connection opened successfully')
      }

      // Add cleanup for when controller is aborted
      controller.signal.addEventListener('abort', () => {
        if (eventSourceRef.current) {
          try {
            eventSourceRef.current.close()
            eventSourceRef.current = null
          } catch (e) {
            console.warn('Error closing EventSource during abort:', e)
          }
        }
      })

    } catch (error) {
      console.error('Failed to create SSE connection:', error)
      setConnected(false)
      scheduleReconnect()
    }
  }, [])

  const scheduleReconnect = useCallback(() => {
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      if (enabledRef.current && !abortRef.current?.signal.aborted) {
        connect()
      }
    }, 3000)
  }, [connect])

  useEffect(() => {
    // Check if tab is active
    const handleVisibilityChange = () => {
      isTabActiveRef.current = !document.hidden
      
      // If tab became active and we should be connected, reconnect
      if (isTabActiveRef.current && enabledRef.current && !abortRef.current?.signal.aborted) {
        connect()
      }
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    
    if (!enabled) {
      // Clean up when disabled
      if (eventSourceRef.current) {
        try {
          eventSourceRef.current.close()
        } catch (e) {
          console.warn('Error closing EventSource during disable:', e)
        }
        eventSourceRef.current = null
      }
      
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
      if (timerRef.current) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      setConnected(false)
      return
    }

    connect()

    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange)
      
      // Clean up all resources when component unmounts
      if (eventSourceRef.current) {
        try {
          eventSourceRef.current.close()
        } catch (e) {
          console.warn('Error closing EventSource during cleanup:', e)
        }
        eventSourceRef.current = null
      }
      
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
      if (timerRef.current) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      setConnected(false)
    }
  }, [enabled, connect])

  return { connected, reconnect: connect }
}