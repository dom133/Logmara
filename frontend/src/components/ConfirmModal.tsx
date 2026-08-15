import React, { useState } from 'react';
import { Modal, Button } from 'antd';

interface ConfirmModalProps {
  open: boolean;
  title: string;
  content: string;
  confirmText?: string;
  confirmType?: 'primary' | 'danger';
  onConfirm: () => void;
  onCancel: () => void;
  confirming?: boolean;
}

const ConfirmModal: React.FC<ConfirmModalProps> = ({
  open,
  title,
  content,
  confirmText = 'Confirm',
  confirmType = 'danger',
  onConfirm,
  onCancel,
  confirming = false,
}) => (
  <Modal
    title={title}
    open={open}
    onOk={onConfirm}
    onCancel={onCancel}
    okText={confirmText}
    okType={confirmType}
    confirmLoading={confirming}
  >
    <p>{content}</p>
  </Modal>
);

export default ConfirmModal;