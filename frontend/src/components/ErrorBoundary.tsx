import React, { Component, ErrorInfo, ReactNode } from 'react';
import { Alert, Button, Result, Typography } from 'antd';

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
          title="Something went wrong"
          subTitle={this.state.error?.message || 'An unexpected error occurred'}
          extra={
            <>
              <Button type="primary" onClick={this.handleReset}>
                Try Again
              </Button>
              <Alert
                style={{ marginTop: 16, maxWidth: 600 }}
                message="Error Details"
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