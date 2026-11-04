# 用户页面检查

`npm install` 后执行 `npm test`，覆盖页面渲染、带版本的记录操作、北京时间、完整提示词保留、外部文本转义和建议确认边界。

`npm run preview:synthetic` 在 `127.0.0.1:18984` 提供合成数据预览。它不会连接真实助手、邮箱、模型或微信，不应作为生产服务部署。预览数据位于 `robot-fixtures.cjs`。

真实数据来自既有 owner-scoped native bridge；前端不维护另一套业务存储。建议确认仍通过真实对话完成，不直接调用缺少对话授权的 approve 接口。
