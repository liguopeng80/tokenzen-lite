import { useState, useCallback } from 'react';
import { Form, type FormInstance } from 'antd';
import { message } from '../feedback';

export interface UseModalFormOptions {
  onSubmit: (values: Record<string, unknown>) => Promise<void>;
  onSuccess?: () => void;
}

export interface UseModalFormReturn {
  open: boolean;
  loading: boolean;
  form: FormInstance;
  show: (initialValues?: Record<string, unknown>) => void;
  close: () => void;
  handleOk: () => void;
}

export function useModalForm(options: UseModalFormOptions): UseModalFormReturn {
  const { onSubmit, onSuccess } = options;
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [form] = Form.useForm();

  const show = useCallback(
    (initialValues?: Record<string, unknown>) => {
      form.resetFields();
      if (initialValues) {
        form.setFieldsValue(initialValues);
      }
      setOpen(true);
    },
    [form],
  );

  const close = useCallback(() => {
    setOpen(false);
    form.resetFields();
  }, [form]);

  const handleOk = useCallback(async () => {
    try {
      const values = await form.validateFields();
      setLoading(true);
      await onSubmit(values);
      setOpen(false);
      form.resetFields();
      onSuccess?.();
    } catch (err) {
      // Ant Design validation errors have errorFields
      if (err && typeof err === 'object' && 'errorFields' in err) {
        return; // form validation failed — stay open, Ant Design shows errors
      }
      // Submit error — show message, stay open
      const msg = err instanceof Error ? err.message : '操作失败';
      message.error(msg);
    } finally {
      setLoading(false);
    }
  }, [form, onSubmit, onSuccess]);

  return { open, loading, form, show, close, handleOk };
}
