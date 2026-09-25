import { useEffect, useState } from 'react'
import {
  Table,
  Card,
  Button,
  Modal,
  Form,
  Space,
  Popconfirm,
  message,
  Typography,
  Select,
  DatePicker,
  InputNumber,
  Tabs,
  Radio,
  Tag,
  Alert,
  Input,
} from 'antd'
import { PlusOutlined, EditOutlined, DeleteOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import { paymentApi, studentApi, courseApi, refundApi, RefundQuota } from '@/services/api'

const { Title, Text } = Typography
const { Option } = Select
const { TextArea } = Input

const paymentMethods = [
  { value: 'cash', label: '现金' },
  { value: 'wechat', label: '微信' },
  { value: 'alipay', label: '支付宝' },
  { value: 'bank', label: '银行转账' },
]

const paymentTypes = [
  { value: 'tuition', label: '学费' },
  { value: 'deposit', label: '定金' },
  { value: 'other', label: '其他' },
]

const refundStatusMap: Record<string, { label: string; color: string }> = {
  pending: { label: '审批中', color: 'orange' },
  approved: { label: '已同意', color: 'green' },
  rejected: { label: '已驳回', color: 'red' },
}

function Finance() {
  const [loading, setLoading] = useState(false)
  const [payments, setPayments] = useState<any[]>([])
  const [students, setStudents] = useState<any[]>([])
  const [courses, setCourses] = useState<any[]>([])
  const [modalVisible, setModalVisible] = useState(false)
  const [modalType, setModalType] = useState<'create' | 'edit'>('create')
  const [selectedPayment, setSelectedPayment] = useState<any>(null)
  const [form] = Form.useForm()

  const fetchPayments = async () => {
    try {
      setLoading(true)
      const res: any = await paymentApi.list({ page_size: 1000 })
      setPayments(res.list || [])
    } catch (error) {
      console.error('Fetch payments error:', error)
    } finally {
      setLoading(false)
    }
  }

  const fetchOptions = async () => {
    try {
      const [studentsRes, coursesRes] = await Promise.all([
        studentApi.list({ page_size: 1000 }),
        courseApi.list(),
      ])
      setStudents((studentsRes as any)?.list || [])
      setCourses((coursesRes as any)?.list || [])
    } catch (error) {
      console.error('Fetch options error:', error)
    }
  }

  useEffect(() => {
    fetchPayments()
    fetchOptions()
  }, [])

  const handleCreate = () => {
    setModalType('create')
    setSelectedPayment(null)
    form.resetFields()
    form.setFieldsValue({
      payment_date: dayjs(),
      payment_method: 'wechat',
      type: 'tuition',
    })
    setModalVisible(true)
  }

  const handleEdit = (payment: any) => {
    setModalType('edit')
    setSelectedPayment(payment)
    form.setFieldsValue({
      ...payment,
      payment_date: payment.payment_date ? dayjs(payment.payment_date) : undefined,
    })
    setModalVisible(true)
  }

  const handleDelete = async (id: number) => {
    try {
      await paymentApi.delete(id)
      message.success('删除成功')
      fetchPayments()
    } catch (error) {
      console.error('Delete payment error:', error)
    }
  }

  const handleModalSubmit = async () => {
    try {
      const values = await form.validateFields()
      const data = {
        ...values,
        payment_date: values.payment_date.format('YYYY-MM-DD'),
      }

      if (modalType === 'create') {
        await paymentApi.create(data)
        message.success('创建成功')
      } else if (selectedPayment?.id) {
        await paymentApi.update(selectedPayment.id, data)
        message.success('更新成功')
      }
      setModalVisible(false)
      fetchPayments()
    } catch (error) {
      console.error('Modal submit error:', error)
    }
  }

  const courseName = (id?: number | null) =>
    courses.find((c) => c.id === id)?.name || (id ? `课程#${id}` : '-')

  const columns = [
    {
      title: '学员',
      dataIndex: ['student', 'name'],
      key: 'student',
      render: (name: string) => name || '-',
    },
    {
      title: '课程',
      dataIndex: ['course', 'name'],
      key: 'course',
      render: (name: string) => name || '-',
    },
    {
      title: '金额(元)',
      dataIndex: 'amount',
      key: 'amount',
      render: (amount: number) => (
        <span style={{ color: amount < 0 ? '#cf1322' : undefined }}>
          {amount < 0 ? amount.toFixed(2) : amount}
        </span>
      ),
    },
    {
      title: '支付方式',
      dataIndex: 'payment_method',
      key: 'payment_method',
      render: (method: string) => {
        const opt = paymentMethods.find((o) => o.value === method)
        return opt?.label || method
      },
    },
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
      render: (type: string) => {
        if (type === 'refund') return <Tag color="red">退费</Tag>
        const opt = paymentTypes.find((o) => o.value === type)
        return opt?.label || type
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      render: (status: string) =>
        status === 'refunded' ? <Tag color="red">已退费</Tag> : <Tag color="green">已缴费</Tag>,
    },
    {
      title: '日期',
      dataIndex: 'payment_date',
      key: 'payment_date',
    },
    {
      title: '收据号',
      dataIndex: 'receipt_no',
      key: 'receipt_no',
    },
    {
      title: '操作',
      key: 'action',
      render: (_: any, record: any) => {
        const readonly = record.type === 'refund' || record.status === 'refunded'
        return (
          <Space size="small">
            <Button
              type="link"
              size="small"
              disabled={readonly}
              onClick={() => handleEdit(record)}
            >
              <EditOutlined /> 编辑
            </Button>
            <Popconfirm
              title="确定删除?"
              onConfirm={() => handleDelete(record.id!)}
              okText="确定"
              cancelText="取消"
            >
              <Button type="link" size="small" danger>
                <DeleteOutlined /> 删除
              </Button>
            </Popconfirm>
          </Space>
        )
      },
    },
  ]

  return (
    <div>
      <Title level={3} style={{ marginBottom: 24 }}>
        财务管理
      </Title>

      <Tabs
        items={[
          {
            key: 'payments',
            label: '缴费记录',
            children: (
              <Card>
                <div
                  style={{
                    marginBottom: 16,
                    display: 'flex',
                    justifyContent: 'flex-end',
                  }}
                >
                  <Button type="primary" icon={<PlusOutlined />} onClick={handleCreate}>
                    新增缴费
                  </Button>
                </div>

                <Table
                  columns={columns}
                  dataSource={payments}
                  rowKey="id"
                  loading={loading}
                />
              </Card>
            ),
          },
          {
            key: 'refunds',
            label: '退费申请',
            children: (
              <RefundTab
                students={students}
                courseName={courseName}
                onChanged={fetchPayments}
              />
            ),
          },
        ]}
      />

      <Modal
        title={modalType === 'create' ? '新增缴费' : '编辑缴费'}
        open={modalVisible}
        onOk={handleModalSubmit}
        onCancel={() => setModalVisible(false)}
        destroyOnClose
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="student_id"
            label="学员"
            rules={[{ required: true, message: '请选择学员' }]}
          >
            <Select placeholder="请选择学员" showSearch optionFilterProp="children">
              {students.map((s) => (
                <Option key={s.id} value={s.id}>
                  {s.name}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item name="course_id" label="课程">
            <Select placeholder="请选择课程" allowClear>
              {courses.map((c) => (
                <Option key={c.id} value={c.id}>
                  {c.name}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="amount"
            label="金额(元)"
            rules={[{ required: true, message: '请输入金额' }]}
          >
            <InputNumber
              style={{ width: '100%' }}
              min={0}
              precision={2}
              placeholder="请输入金额"
            />
          </Form.Item>
          <Form.Item
            name="payment_method"
            label="支付方式"
            rules={[{ required: true, message: '请选择支付方式' }]}
          >
            <Radio.Group>
              {paymentMethods.map((m) => (
                <Radio key={m.value} value={m.value}>
                  {m.label}
                </Radio>
              ))}
            </Radio.Group>
          </Form.Item>
          <Form.Item
            name="type"
            label="类型"
            rules={[{ required: true, message: '请选择类型' }]}
          >
            <Select placeholder="请选择类型">
              {paymentTypes.map((t) => (
                <Option key={t.value} value={t.value}>
                  {t.label}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="payment_date"
            label="缴费日期"
            rules={[{ required: true, message: '请选择日期' }]}
          >
            <DatePicker style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="remarks" label="备注">
            <TextArea rows={2} placeholder="请输入备注" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

function RefundTab({
  students,
  courseName,
  onChanged,
}: {
  students: any[]
  courseName: (id?: number | null) => string
  onChanged: () => void
}) {
  const [loading, setLoading] = useState(false)
  const [refunds, setRefunds] = useState<any[]>([])
  const [modalVisible, setModalVisible] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [studentCourses, setStudentCourses] = useState<any[]>([])
  const [quota, setQuota] = useState<RefundQuota | null>(null)
  const [quotaLoading, setQuotaLoading] = useState(false)
  const [form] = Form.useForm()

  const fetchRefunds = async () => {
    try {
      setLoading(true)
      const res: any = await refundApi.list()
      setRefunds(res || [])
    } catch (error) {
      console.error('Fetch refunds error:', error)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchRefunds()
  }, [])

  const handleOpen = () => {
    form.resetFields()
    setStudentCourses([])
    setQuota(null)
    setModalVisible(true)
  }

  const handleStudentChange = async (studentId: number) => {
    form.setFieldsValue({ course_id: undefined, amount: undefined })
    setQuota(null)
    try {
      const detail: any = await studentApi.get(studentId)
      setStudentCourses(detail?.courses || [])
    } catch (error) {
      console.error('Fetch student detail error:', error)
    }
  }

  const handleCourseChange = async (courseId: number) => {
    form.setFieldsValue({ amount: undefined })
    const studentId = form.getFieldValue('student_id')
    if (!studentId || !courseId) return
    try {
      setQuotaLoading(true)
      const res = await refundApi.quota(studentId, courseId)
      setQuota(res)
      if (res.refunded) {
        message.warning('该课程已退费')
      }
    } catch (error) {
      setQuota(null)
      console.error('Fetch quota error:', error)
    } finally {
      setQuotaLoading(false)
    }
  }

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields()
      setSubmitting(true)
      await refundApi.create({
        student_id: values.student_id,
        course_id: values.course_id,
        amount: values.amount,
        reason: values.reason,
      })
      message.success('退费申请已提交，等待财务审批')
      setModalVisible(false)
      fetchRefunds()
      onChanged()
    } catch (error) {
      console.error('Create refund error:', error)
    } finally {
      setSubmitting(false)
    }
  }

  const handleProcess = async (id: number, status: 'approved' | 'rejected') => {
    try {
      await refundApi.process(id, { status })
      message.success(status === 'approved' ? '已同意退费' : '已驳回申请')
      fetchRefunds()
      onChanged()
    } catch (error) {
      console.error('Process refund error:', error)
    }
  }

  const columns = [
    {
      title: '学员',
      dataIndex: ['student', 'name'],
      key: 'student',
      render: (name: string) => name || '-',
    },
    {
      title: '课程',
      dataIndex: 'course_id',
      key: 'course_id',
      render: (id: number) => courseName(id),
    },
    {
      title: '退费金额(元)',
      dataIndex: 'amount',
      key: 'amount',
      render: (v: number) => <Text strong>{v?.toFixed?.(2) ?? v}</Text>,
    },
    {
      title: '申请时课时',
      key: 'hours',
      render: (_: any, r: any) =>
        `剩余 ${Math.max((r.total_hours || 0) - (r.used_hours || 0), 0)} / 共 ${
          r.total_hours || 0
        } 课时`,
    },
    {
      title: '原因',
      dataIndex: 'reason',
      key: 'reason',
      render: (reason: string) => reason || '-',
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      render: (status: string) => {
        const s = refundStatusMap[status]
        return <Tag color={s?.color}>{s?.label || status}</Tag>
      },
    },
    {
      title: '申请人',
      dataIndex: ['applied_user', 'name'],
      key: 'applied_user',
      render: (name: string) => name || '-',
    },
    {
      title: '申请时间',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (v: string) => (v ? v.replace('T', ' ').slice(0, 16) : '-'),
    },
    {
      title: '退费日期',
      dataIndex: 'refund_date',
      key: 'refund_date',
      render: (v?: string) => v || '-',
    },
    {
      title: '操作',
      key: 'action',
      render: (_: any, record: any) =>
        record.status === 'pending' ? (
          <Space size="small">
            <Popconfirm
              title="确定同意该退费申请？"
              description="同意后将生成负数退费流水并清零剩余课时"
              onConfirm={() => handleProcess(record.id, 'approved')}
              okText="同意"
              cancelText="取消"
              okButtonProps={{ danger: true }}
            >
              <Button type="link" size="small">
                同意
              </Button>
            </Popconfirm>
            <Popconfirm
              title="确定驳回该退费申请？"
              onConfirm={() => handleProcess(record.id, 'rejected')}
              okText="驳回"
              cancelText="取消"
            >
              <Button type="link" size="small" danger>
                驳回
              </Button>
            </Popconfirm>
          </Space>
        ) : (
          '-'
        ),
    },
  ]

  return (
    <Card>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'flex-end' }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={handleOpen}>
          退费申请
        </Button>
      </div>

      <Table columns={columns} dataSource={refunds} rowKey="id" loading={loading} />

      <Modal
        title="退费申请"
        open={modalVisible}
        onOk={handleSubmit}
        confirmLoading={submitting}
        onCancel={() => setModalVisible(false)}
        destroyOnClose
        okText="提交申请"
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="student_id"
            label="学员"
            rules={[{ required: true, message: '请选择学员' }]}
          >
            <Select
              placeholder="请选择学员"
              showSearch
              optionFilterProp="children"
              onChange={handleStudentChange}
            >
              {students.map((s) => (
                <Option key={s.id} value={s.id}>
                  {s.name}
                </Option>
              ))}
            </Select>
          </Form.Item>
          <Form.Item
            name="course_id"
            label="退学课程"
            rules={[{ required: true, message: '请选择课程' }]}
          >
            <Select
              placeholder="请选择课程"
              onChange={handleCourseChange}
              loading={quotaLoading}
              notFoundContent={
                form.getFieldValue('student_id') ? '该学员暂无可退课程' : '请先选择学员'
              }
            >
              {studentCourses
                .filter((sc) => sc.status !== 2)
                .map((sc) => (
                  <Option key={sc.course_id} value={sc.course_id}>
                    {courseName(sc.course_id)}
                  </Option>
                ))}
            </Select>
          </Form.Item>

          {quota && (
            <Alert
              style={{ marginBottom: 16 }}
              type={quota.refunded || quota.available_amount <= 0 ? 'warning' : 'info'}
              showIcon
              message={
                <div>
                  <div>
                    未上课时：<Text strong>{quota.remaining_hours}</Text> / {quota.total_hours}{' '}
                    课时，单价 {quota.price_per_hour} 元/课时
                  </div>
                  <div>
                    最多可退：
                    <Text strong>{quota.max_refund_amount.toFixed(2)}</Text> 元
                    {quota.pending_amount > 0 && (
                      <Text type="warning">
                        {' '}
                        （审批中已占用 {quota.pending_amount.toFixed(2)} 元）
                      </Text>
                    )}
                  </div>
                  <div>
                    当前可申请：
                    <Text strong type={quota.available_amount > 0 ? 'success' : 'danger'}>
                      {quota.available_amount.toFixed(2)}
                    </Text>{' '}
                    元
                  </div>
                </div>
              }
            />
          )}

          <Form.Item
            name="amount"
            label="退费金额(元)"
            rules={[{ required: true, message: '请输入退费金额' }]}
          >
            <InputNumber
              style={{ width: '100%' }}
              min={0.01}
              max={quota?.available_amount || undefined}
              precision={2}
              placeholder="不能超过当前可申请金额"
            />
          </Form.Item>
          <Form.Item name="reason" label="退学/退费原因">
            <TextArea rows={3} placeholder="请输入原因" />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}

export default Finance
