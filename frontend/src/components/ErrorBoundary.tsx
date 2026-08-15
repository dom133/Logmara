import React, { Component, ErrorInfo, ReactNode } from 'react';
import { Alert, Button, Result, Typography } from 'antd';
import i18n from '../i18n';

const { Title } = Typography;

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback?: ReactNode;
}

interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
}

class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('Uncaught error:', error, errorInfo);
  }

  handleReset = () => {
    this.setState({ hasError: false, error: null });
  };

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) {
        return this.props.fallback;
      }

      return (
        <Result
          status="error"
          title={i18n.t('errorBoundary.title')}
          subTitle={this.state.error?.message || i18n.t('errorBoundary.defaultMessage')}
          extra={
            <>
              <Button type="primary" onClick={this.handleReset}>
                {i18n.t('errorBoundary.tryAgain')}
              </Button>
              <Alert
                style={{ marginTop: 16, maxWidth: 600 }}
                message={i18n.t('errorBoundary.errorDetails')}
                description={this.state.error?.toString()}
                type="error"
                showIcon
              />
            </>
          }
        />
      );
    }

    return this.props.children;
  }
}

export default ErrorBoundary;