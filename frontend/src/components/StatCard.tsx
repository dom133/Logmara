import React from 'react';
import { Card, Typography } from 'antd';
import { ReactNode } from 'react';

const { Title, Text } = Typography;

interface StatCardProps {
  title: string;
  value: string | number;
  icon?: ReactNode;
  color?: string;
  subtitle?: string;
}

const StatCard: React.FC<StatCardProps> = ({ title, value, icon, color = '#1890ff', subtitle }) => (
  <Card hoverable style={{ width: '100%', height: '100%' }}>
    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
      {icon && <div style={{ fontSize: 24, color, flexShrink: 0 }}>{icon}</div>}
      <div style={{ minWidth: 0, flex: 1 }}>
        <Text type="secondary">{title}</Text>
        <Title level={3} style={{ margin: '4px 0 0 0', color }}>
          {value}
        </Title>
        {/* Always render this line, even when there's no subtitle, so every card
            has the same number of text lines and they come out the same height
            without needing to measure anything. */}
        <Text type="secondary" style={subtitle ? undefined : { visibility: 'hidden' }}>
          {subtitle || ' '}
        </Text>
      </div>
    </div>
  </Card>
);

export default StatCard;