import React from 'react';
import { Tag, Typography } from 'antd';
import { SEVERITY_COLORS, SEVERITY_LABELS } from '../constants';

const { Text } = Typography;

interface SeverityTagProps {
  severity: string;
}

const SeverityTag: React.FC<SeverityTagProps> = ({ severity }) => {
  const color = SEVERITY_COLORS[severity.toLowerCase()] || SEVERITY_COLORS['info'];
  const label = SEVERITY_LABELS[severity.toLowerCase()] || severity;

  return (
    <Tag color={color}>
      <Text strong>{label}</Text>
    </Tag>
  );
};

export default SeverityTag;