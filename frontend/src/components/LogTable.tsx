import { useState, useMemo } from 'react'
import { Table, Button, Select, Text, Tag } from 'antd'
import { ColumnsType } from 'antd/es/table'
import { LogEntry, DeviceStats, resolveDeviceDisplayName } from '../services/api'
import SeverityTag from './SeverityTag'

export interface LogTableProps {
  logs: LogEntry[]
  devices: DeviceStats[]
  columns?: ColumnsType<LogEntry>
  loading: boolean
  loadingMore: boolean
  hasMore: boolean
  pageSize: number
  setPageSize: (v: number) => void
  onLoadMore: () => void
  onRowClick: (record: LogEntry) => void
  enhanceColumns?: (cols: ColumnsType<LogEntry>) => ColumnsType<LogEntry>
}

export function useDeviceMap(devices: DeviceStats[]): Map<string, string> {
  return useMemo(() => {
    const m = new Map<string, string>()
    for (const d of devices) {
      const dn = resolveDeviceDisplayName(d)
      if (d.fromhost_ip) m.set(d.fromhost_ip, dn)
      if (d.hostname) m.set(d.hostname, dn)
      if (d.old_hostname) m.set(d.old_hostname, dn)
    }
    return m
  }, [devices])
}

export function resolveHostname(
  deviceMap: Map<string, string>,
  hostname: string,
  fromhost_ip?: string,
  displayName?: string,
): string {
  if (displayName) return displayName
  return deviceMap.get(fromhost_ip || hostname || '') || hostname || '-'
}

export function buildDefaultColumns(deviceMap: Map<string, string>): ColumnsType<LogEntry> {

  return [
    {
      title: 'Time',
      dataIndex: 'timestamp',
      key: 'timestamp',
      width: 180,
      render: (v: string) => new Date(v).toLocaleString(),
    },
    {
      title: 'Severity',
      dataIndex: 'severity',
      key: 'severity',
      width: 90,
      render: (v: string) => <SeverityTag severity={v} />,
    },
    {
      title: 'Device',
      dataIndex: 'hostname',
      key: 'hostname',
      width: 160,
      render: (v: string | null | undefined, r: LogEntry) =>
        v ? (
          <Tag color="blue">{resolveHostname(deviceMap, v, r.fromhost_ip, r.display_name)}</Tag>
        ) : (
          '-'
        ),
    },
    {
      title: 'App',
      dataIndex: 'app_name',
      key: 'app_name',
      width: 120,
      render: (v?: string) => (v ? <Text type="secondary">{v}</Text> : '-'),
    },
    {
      title: 'Message',
      dataIndex: 'message',
      key: 'message',
      width: 300,
      ellipsis: { showTitle: true },
      render: (v: string, record: LogEntry) => {
        const display = record.raw_message || v
        return (
          <pre
            style={{
              margin: 0,
              width: '100%',
              maxWidth: '100%',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              fontFamily: 'Consolas, Monaco, monospace',
              fontSize: 12,
              lineHeight: 1.4,
              maxHeight: 100,
              overflow: 'auto',
            }}
          >
            {display}
          </pre>
        )
      },
    },
  ]
}

export default function LogTable({
  logs,
  devices,
  columns,
  loading,
  loadingMore,
  hasMore,
  pageSize,
  setPageSize,
  onLoadMore,
  onRowClick,
  enhanceColumns,
}: LogTableProps) {
  const deviceMap = useDeviceMap(devices)
  const defaultCols = buildDefaultColumns(deviceMap)
  const finalColumns = columns || defaultCols

  const renderedColumns = enhanceColumns ? enhanceColumns(finalColumns) : finalColumns

  return (
    <>
      <Table
        columns={renderedColumns}
        dataSource={logs}
        rowKey="id"
        loading={loading}
        tableLayout="fixed"
        scroll={{ x: 'max-content' }}
        pagination={false}
        size="small"
        onRow={(record) => ({
          onClick: () => onRowClick(record),
          style: { cursor: 'pointer' },
        })}
      />
      <div
        style={{
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          gap: 8,
          marginTop: 16,
        }}
      >
        <Select
          size="small"
          style={{ width: 100 }}
          value={pageSize}
          onChange={setPageSize}
          options={['25', '50', '100', '200'].map(v => ({
            label: `${v} / page`,
            value: parseInt(v),
          }))}
        />
        {hasMore ? (
          <Button onClick={onLoadMore} loading={loadingMore}>
            Load more
          </Button>
        ) : (
          <Text type="secondary">No more logs</Text>
        )}
      </div>
    </>
  )
}
