import React, { useState } from 'react';
import { Modal, Form, Input, Button, message } from 'antd';
import { api } from '../services/api';

interface PasswordResetModalProps {
  open: boolean;
  username: string;
  onClose: () => void;
  onReset: () => void;
}

const PasswordResetModal: React.FC<PasswordResetModalProps> = ({ open, username, onClose, onReset }) => {
  const [form] = Form.useForm();
  const [confirming, setConfirming] = useState(false);

  const handleReset = async () => {
    try {
      const values = await form.validateFields();
      if (values.newPassword !== values.confirmPassword) {
        message.error('Passwords do not match');
        return;
      }
      setConfirming(true);
      await api.post('/auth/change-password', {
        username: username,
        oldPassword: values.oldPassword,
        newPassword: values.newPassword,
      });
      message.success('Password reset successful');
      form.resetFields();
      onReset();
      onClose();
    } catch (err: any) {
      message.error(err.response?.data?.message || 'Failed to reset password');
    } finally {
      setConfirming(false);
    }
  };

  return (
    <Modal
      title={`Reset Password for ${username}`}
      open={open}
      onCancel={onClose}
      footer={null}
    >
      <Form form={form} layout="vertical">
        <Form.Item
          name="oldPassword"
          label="Current Password"
          rules={[{ required: true, message: 'Enter current password' }]}
        >
          <Input.Password placeholder="Current password" />
        </Form.Item>
        <Form.Item
          name="newPassword"
          label="New Password"
          rules={[{ required: true, message: 'Enter new password' }, { min: 8, message: 'At least 8 characters' }]}
        >
          <Input.Password placeholder="New password" />
        </Form.Item>
        <Form.Item
          name="confirmPassword"
          label="Confirm New Password"
          rules={[{ required: true, message: 'Confirm your new password' }]}
        >
          <Input.Password placeholder="Confirm new password" />
        </Form.Item>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="primary" onClick={handleReset} loading={confirming}>
            Reset Password
          </Button>
        </div>
      </Form>
    </Modal>
  );
};

export default PasswordResetModal;