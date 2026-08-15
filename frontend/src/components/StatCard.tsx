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
  <Card hoverable style={{ width: '100%' }}>
    <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
      {icon && <div style={{ fontSize: 24, color }}>{icon}</div>}
      <div style={{ minWidth: 0, flex: 1 }}>
        <Text type="secondary">{title}</Text>
        <Title level={3} style={{ margin: '4px 0 0 0', color }}>
          {value}
        </Title>
        {subtitle && <Text type="secondary">{subtitle}</Text>}
      </div>
    </div>
  </Card>
);

export default StatCard;