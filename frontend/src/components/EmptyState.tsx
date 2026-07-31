import React from 'react';
import { Empty, Button, Typography } from 'antd';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

interface EmptyStateProps {
  description?: string;
  actionLabel?: string;
  actionPath?: string;
  actionClick?: () => void;
  imageHeight?: number;
}

const EmptyState: React.FC<EmptyStateProps> = ({
  description,
  actionLabel,
  actionPath,
  actionClick,
  imageHeight = 60,
}) => {
  const { t } = useTranslation();
  return (
    <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} imageStyle={{ height: imageHeight }} description={
      <>
        <Text type="secondary">{description ?? t('common.noData')}</Text>
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
};

export default EmptyState;