import React from 'react';
import { Empty, Button, Typography } from 'antd';
import { Link } from 'react-router-dom';

const { Text } = Typography;

interface EmptyStateProps {
  description?: string;
  actionLabel?: string;
  actionPath?: string;
  actionClick?: () => void;
  imageHeight?: number;
}

const EmptyState: React.FC<EmptyStateProps> = ({
  description = 'No data available',
  actionLabel,
  actionPath,
  actionClick,
  imageHeight = 60,
}) => (
  <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} imageStyle={{ height: imageHeight }} description={
    <>
      <Text type="secondary">{description}</Text>
      {(actionLabel && actionPath) && (
        <div style={{ marginTop: 16 }}>
          <Button type="primary">
            <Link to={actionPath}>{actionLabel}</Link>
          </Button>
        </div>
      )}
      {actionClick && (
        <div style={{ marginTop: 16 }}>
          <Button type="primary" onClick={actionClick}>
            {actionLabel}
          </Button>
        </div>
      )}
    </>
  } />
);

export default EmptyState;