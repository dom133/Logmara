import React from 'react';
import { Tag, Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { SEVERITY_COLORS, getSeverityLabels } from '../constants';

const { Text } = Typography;

interface SeverityTagProps {
  severity: string;
}

const SeverityTag: React.FC<SeverityTagProps> = ({ severity }) => {
  const { t } = useTranslation();
  const color = SEVERITY_COLORS[severity.toLowerCase()] || SEVERITY_COLORS['info'];
  const label = getSeverityLabels(t)[severity.toLowerCase()] || severity;

  return (
    <Tag color={color}>
      <Text strong>{label}</Text>
    </Tag>
  );
};

export default SeverityTag;