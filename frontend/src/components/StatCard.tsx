import React from 'react';
import { Card, Typography } from 'antd';
import { ReactNode } from 'react';
import { tokens } from '../theme/tokens';

const { Title, Text } = Typography;

interface StatCardProps {
  title: string;
  value: string | number;
  icon?: ReactNode;
  color?: string;
  subtitle?: string;
}

const StatCard: React.FC<StatCardProps> = ({ title, value, icon, color = tokens.colors.primary, subtitle }) => (
  <Card
    hoverable
    className="transition-hover"
    style={{
      width: '100%',
      height: '100%',
      boxShadow: tokens.shadow.card,
    }}
  >
    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
      {icon && <div style={{ fontSize: 24, color, flexShrink: 0 }}>{icon}</div>}
      <div style={{ minWidth: 0, flex: 1 }}>
        <Text type="secondary">{title}</Text>
        <Title level={3} style={{ margin: '4px 0 0 0', color }}>
          {value}
        </Title>
        <Text type="secondary" style={subtitle ? undefined : { visibility: 'hidden' }}>
          {subtitle || '\u00A0'}
        </Text>
      </div>
    </div>
  </Card>
);

export default StatCard;
