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

// Ordered worst-to-best; drives severity sort order across the UI.
export const SEVERITY_ORDER = ['emerg', 'alert', 'crit', 'err', 'warning', 'notice', 'info', 'debug'];

// Hex equivalents of SEVERITY_COLORS for use outside antd <Tag> (e.g. chart itemStyle),
// where antd color names like 'default' aren't valid CSS and 'magenta' collides between emerg/alert.
export const SEVERITY_HEX: Record<string, string> = {
  emerg: '#9254de',
  alert: '#eb2f96',
  crit: '#f5222d',
  err: '#fa8c16',
  warning: '#faad14',
  notice: '#1890ff',
  info: '#13c2c2',
  debug: '#8c8c8c',
};

export const DATE_PRESETS = [
  { label: 'Last Hour', value: '1h' },
  { label: 'Last 6 Hours', value: '6h' },
  { label: 'Last 24 Hours', value: '24h' },
  { label: 'Last 7 Days', value: '7d' },
  { label: 'Last 30 Days', value: '30d' },
];