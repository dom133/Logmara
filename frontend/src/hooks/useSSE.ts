import { useEffect, useRef, useState, useCallback } from 'react'
import { LogEntry } from '../services/api'

interface UseSSEOptions {
  onNewLogs: (logs: LogEntry[]) => void
  filters?: {
    hostname?: string
    severity?: string
    search?: string
    from?: string
    to?: string
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
  onNewLogsRef.current = onNewLogs
  filtersRef.current = filters

  const connect = useCallback(() => {
    if (abortRef.current) {
      abortRef.current.abort()
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

    fetch(url.toString(), {
      method: 'GET',
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: 'text/event-stream',
      },
      signal: controller.signal,
    })
      .then(async (response) => {
        if (!response.ok) {
          setConnected(false)
          return
        }

        setConnected(true)
        const reader = response.body?.getReader()
        if (!reader) {
          setConnected(false)
          return
        }

        const decoder = new TextDecoder()
        let buffer = ''

        try {
          while (true) {
            const { done, value } = await reader.read()
            if (done) break

            buffer += decoder.decode(value, { stream: true })
            const lastDoubleNewline = buffer.lastIndexOf('\n\n')
            if (lastDoubleNewline === -1) continue

            const complete = buffer.slice(0, lastDoubleNewline + 2)
            buffer = buffer.slice(lastDoubleNewline + 2)
            const events = parseSSE(complete)

            for (const evt of events) {
              if (evt.event === 'log' && evt.data) {
                try {
                  const logs = JSON.parse(evt.data) as LogEntry[]
                  if (Array.isArray(logs) && logs.length > 0) {
                    onNewLogsRef.current(logs)
                  }
                } catch {
                  // ignore parse errors
                }
              }
            }
          }
        } catch (e: any) {
          if (e.name !== 'AbortError') {
            console.error('SSE stream error:', e)
          }
        } finally {
          setConnected(false)
          reader.releaseLock()
        }
      })
      .catch((e: any) => {
        if (e.name !== 'AbortError') {
          setConnected(false)
        }
      })
  }, [])

  useEffect(() => {
    if (!enabled) {
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
      setConnected(false)
      return
    }

    connect()

    return () => {
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
      setConnected(false)
    }
  }, [enabled])

  return { connected, reconnect: connect }
}