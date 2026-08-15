import { TagProps } from 'antd';
import { TFunction } from 'i18next';
import { tokens } from './theme/tokens';

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

export function getSeverityLabels(t: TFunction): Record<string, string> {
  return {
    emerg: t('severity.emerg'),
    alert: t('severity.alert'),
    crit: t('severity.crit'),
    err: t('severity.err'),
    warning: t('severity.warning'),
    notice: t('severity.notice'),
    info: t('severity.info'),
    debug: t('severity.debug'),
  };
}

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
  notice: tokens.colors.primary,
  info: '#13c2c2',
  debug: '#8c8c8c',
};

export function getDatePresets(t: TFunction) {
  return [
    { label: t('logs.lastHour'), value: '1h' },
    { label: t('logs.last6Hours'), value: '6h' },
    { label: t('logs.last24Hours'), value: '24h' },
    { label: t('logs.last7Days'), value: '7d' },
    { label: t('logs.last30Days'), value: '30d' },
  ];
}