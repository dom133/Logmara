import { TagProps } from 'antd';

export const SEVERITY_COLORS: Record<string, NonNullable<TagProps['color']>> = {
  emerg: 'magenta',
  alert: 'magenta',
  crit: 'red',
  err: 'orange',
  warning: 'gold',
  notice: 'blue',
  info: 'cyan',
  debug: 'default',
};

export const SEVERITY_LABELS: Record<string, string> = {
  emerg: 'Emergency',
  alert: 'Alert',
  crit: 'Critical',
  err: 'Error',
  warning: 'Warning',
  notice: 'Notice',
  info: 'Info',
  debug: 'Debug',
};

export const DATE_PRESETS = [
  { label: 'Last Hour', value: '1h' },
  { label: 'Last 6 Hours', value: '6h' },
  { label: 'Last 24 Hours', value: '24h' },
  { label: 'Last 7 Days', value: '7d' },
  { label: 'Last 30 Days', value: '30d' },
];