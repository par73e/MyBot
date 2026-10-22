# 上游可拉取模型清单 — Reasonix Studio 当前服务商配置

生成时间：2026-10-22 14:59（本地 UTC+08:00）

生成方式：读取 `~/.reasonix/config.toml` 的 `[[providers]]`，按各自 `api_key_env` 从 `~/.reasonix/.env` 取密钥，真实调用 `GET {base_url}/models` 统计。

## 结论

| 上游 | 可拉取数量 | 对应本地 provider |
|---|---:|---|
| `https://maas.qianwenaiapi.com/compatible-mode/v1` | **178** | `ali` / `ali-wzq` / `ali-01` / `ali-02` / `ali-03` |
| `https://api.deepseek.com` | **2** | `deepseek` |
| **合计** | **180** | — |

- 178 条 **无重复 ID**；5 个 `ali*` provider 指向同一个网关，只是密钥不同，所以共用同一份列表。
- 上游每条只返回 `id` / `object` / `created` / `owned_by`，且 `owned_by` 全为 `system`，**不带厂商、能力、上下文窗口或价格信息**。下文「分组」是按模型 ID 命名规则推断的。
- config.toml 里手写的 `models = [...]` 白名单只有 5 个（`ali*`）/ 3 个（`deepseek`），其余靠 Studio 运行时发现；打 ✅ 的即已在白名单内。

## 一、分组统计（网关 178 条）

| 分组 | 数量 | 备注 |
|---|---:|---|
| Qwen 千问（对话/推理） | 74 |  |
| DeepSeek | 8 |  |
| GLM (智谱) | 12 |  |
| Kimi 月之暗面 | 9 |  |
| MiniMax | 5 |  |
| SiliconFlow | 4 |  |
| vanchin (DeepSeek 第三方托管) | 7 |  |
| StepFun 阶跃 | 2 |  |
| Xiaomi MiMo | 1 |  |
| 其他对话模型 | 7 |  |
| Qwen 千问（图像/语音/Omni/向量） | 40 | 非文本对话，模型选择器很可能不展示 |
| Wan 万相 / 图像生成 | 3 | 图像生成 |
| 其他多模态（GUI/图像） | 1 | 非文本对话 |
| 疑似内部/测试条目 | 5 | 名字带 test-/sre-/cmaas-/analysis-，很可能被过滤 |
| **合计** | **178** | |

## 二、全部 178 条明细

### Qwen 千问（对话/推理）（74 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `qwen-1.8b-chat` | 2024-04-29 |  |
| 2 | `qwen-1.8b-longcontext-chat` | 2024-04-29 |  |
| 3 | `qwen-14b-chat` | 2024-04-29 |  |
| 4 | `qwen-72b-chat` | 2024-04-29 |  |
| 5 | `qwen-7b-chat` | 2024-04-29 |  |
| 6 | `qwen-coder-plus-1106` | 2025-07-31 |  |
| 7 | `qwen-deep-research-2025-12-15` | 2026-03-19 |  |
| 8 | `qwen-deep-search-planning` | 2025-10-30 |  |
| 9 | `qwen-flash` | 2026-08-26 |  |
| 10 | `qwen-flash-character` | 2026-04-10 |  |
| 11 | `qwen-flash-character-2026-02-26` | 2026-02-28 |  |
| 12 | `qwen-long` | 2026-07-10 |  |
| 13 | `qwen-max` | 2026-08-26 |  |
| 14 | `qwen-max-0107` | 2024-04-29 |  |
| 15 | `qwen-max-1201` | 2024-04-29 |  |
| 16 | `qwen-max-longcontext` | 2024-04-29 |  |
| 17 | `qwen-mt-flash` | 2025-11-06 |  |
| 18 | `qwen-mt-lite` | 2025-11-20 |  |
| 19 | `qwen-mt-plus` | 2025-10-29 |  |
| 20 | `qwen-mt-uni` | 2026-09-18 |  |
| 21 | `qwen-plus` | 2026-08-26 |  |
| 22 | `qwen-plus-2025-01-25` | 2025-10-14 |  |
| 23 | `qwen-plus-2025-04-28` | 2025-10-14 |  |
| 24 | `qwen-plus-2025-07-14` | 2026-05-25 |  |
| 25 | `qwen-plus-2025-11-05` | 2025-11-25 |  |
| 26 | `qwen-plus-2025-12-01` | 2026-07-06 |  |
| 27 | `qwen-plus-latest` | 2026-06-11 |  |
| 28 | `qwen-turbo-0919` | 2026-02-03 |  |
| 29 | `qwen-turbo-2024-11-01` | 2025-10-14 |  |
| 30 | `qwen1.5-0.5b-chat` | 2026-03-31 |  |
| 31 | `qwen1.5-1.8b-chat` | 2026-03-31 |  |
| 32 | `qwen1.5-110b-chat` | 2026-03-31 |  |
| 33 | `qwen1.5-14b-chat` | 2026-03-31 |  |
| 34 | `qwen1.5-32b-chat` | 2026-03-31 |  |
| 35 | `qwen1.5-72b-chat` | 2026-03-31 |  |
| 36 | `qwen1.5-7b-chat` | 2026-03-31 |  |
| 37 | `qwen2-0.5b-instruct` | 2026-03-31 |  |
| 38 | `qwen2-1.5b-instruct` | 2026-03-31 |  |
| 39 | `qwen2-57b-a14b-instruct` | 2026-03-31 |  |
| 40 | `qwen2-7b-instruct` | 2026-03-31 |  |
| 41 | `qwen2.5-0.5b-instruct` | 2025-09-09 |  |
| 42 | `qwen2.5-1.5b-instruct` | 2025-09-09 |  |
| 43 | `qwen2.5-math-1.5b-instruct` | 2026-03-31 |  |
| 44 | `qwen3-coder-flash` | 2026-02-03 |  |
| 45 | `qwen3-vl-plus` | 2026-07-30 |  |
| 46 | `qwen3.5-122b-a10b` | 2026-07-03 |  |
| 47 | `qwen3.5-27b` | 2026-07-03 |  |
| 48 | `qwen3.5-35b-a3b` | 2026-06-22 |  |
| 49 | `qwen3.5-397b-a17b` | 2026-07-31 |  |
| 50 | `qwen3.5-flash` | 2026-09-09 |  |
| 51 | `qwen3.5-flash-2026-02-23` | 2026-07-06 |  |
| 52 | `qwen3.5-plus` | 2026-10-12 |  |
| 53 | `qwen3.5-plus-2026-02-15` | 2026-10-12 |  |
| 54 | `qwen3.5-plus-2026-04-20` | 2026-10-12 |  |
| 55 | `qwen3.6-27b` | 2026-07-03 |  |
| 56 | `qwen3.6-35b-a3b` | 2026-07-06 |  |
| 57 | `qwen3.6-flash` | 2026-10-22 |  |
| 58 | `qwen3.6-flash-2026-04-16` | 2026-07-06 |  |
| 59 | `qwen3.6-plus` | 2026-09-16 |  |
| 60 | `qwen3.6-plus-2026-04-02` | 2026-09-16 |  |
| 61 | `qwen3.7-flash` | 2026-08-26 |  |
| 62 | `qwen3.7-flash-2026-07-15` | 2026-07-27 |  |
| 63 | `qwen3.7-max` | 2026-08-26 |  |
| 64 | `qwen3.7-max-2026-05-17` | 2026-07-06 |  |
| 65 | `qwen3.7-max-2026-05-20` | 2026-07-05 |  |
| 66 | `qwen3.7-max-2026-06-08` | 2026-07-06 |  |
| 67 | `qwen3.7-max-preview` | 2026-07-06 |  |
| 68 | `qwen3.7-plus` | 2026-08-26 |  |
| 69 | `qwen3.7-plus-2026-05-26` | 2026-08-26 |  |
| 70 | `qwen3.8-2.4t-a95b` | 2026-08-26 |  |
| 71 | `qwen3.8-27b` | 2026-08-26 |  |
| 72 | `qwen3.8-flash` | 2026-08-31 | ✅ |
| 73 | `qwen3.8-max` | 2026-10-15 |  |
| 74 | `qwen3.8-max-0902` | 2026-09-02 | ✅ |

### DeepSeek（8 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `deepseek-r1-distill-llama-70b` | 2025-09-09 |  |
| 2 | `deepseek-r1-distill-qwen-1.5b` | 2025-07-31 |  |
| 3 | `deepseek-v4-flash-0731` | 2026-08-26 |  |
| 4 | `deepseek-v4-pro` | 2026-08-26 |  |
| 5 | `deepseek-v4-pro-0813` | 2026-09-14 |  |
| 6 | `deepseek-v4.1-flash` | 2026-09-15 |  |
| 7 | `deepseek-v4.1-flash-1017` | 2026-10-21 | ✅ |
| 8 | `vanchin/deepseek-ocr` | 2026-10-14 |  |

### GLM (智谱)（12 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `ZHIPU/GLM-5` | 2026-10-14 |  |
| 2 | `ZHIPU/GLM-5.1` | 2026-05-19 |  |
| 3 | `ZHIPU/GLM-5.2` | 2026-08-17 |  |
| 4 | `ZHIPU/GLM-5.3` | 2026-08-18 |  |
| 5 | `ZHIPU/GLM-5.3-Flash` | 2026-09-01 |  |
| 6 | `ZHIPU/GLM-5.3-FlashX` | 2026-09-21 |  |
| 7 | `glm-5` | 2026-08-26 |  |
| 8 | `glm-5.1` | 2026-08-26 |  |
| 9 | `glm-5.2` | 2026-08-26 |  |
| 10 | `glm-5.2-fast-preview` | 2026-09-08 |  |
| 11 | `glm-5.3` | 2026-09-16 | ✅ |
| 12 | `glm-5.3-prime` | 2026-09-28 |  |

### Kimi 月之暗面（9 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `kimi-k2.5` | 2026-08-06 |  |
| 2 | `kimi-k2.6` | 2026-07-25 |  |
| 3 | `kimi-k2.7-code` | 2026-07-27 |  |
| 4 | `kimi-k3` | 2026-08-24 | ✅ |
| 5 | `kimi/kimi-k2.6` | 2026-10-14 |  |
| 6 | `kimi/kimi-k2.7-code` | 2026-10-14 |  |
| 7 | `kimi/kimi-k2.7-code-highspeed` | 2026-10-14 |  |
| 8 | `kimi/kimi-k2.8-preview` | 2026-09-16 |  |
| 9 | `kimi/kimi-k3` | 2026-10-14 |  |

### MiniMax（5 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `MiniMax/MiniMax-M2.1` | 2026-02-13 |  |
| 2 | `MiniMax/MiniMax-M2.5` | 2026-08-17 |  |
| 3 | `MiniMax/MiniMax-M2.7` | 2026-08-17 |  |
| 4 | `MiniMax/MiniMax-M3` | 2026-08-17 |  |
| 5 | `MiniMax/MiniMax-M3.1-Flash` | 2026-10-21 |  |

### SiliconFlow（4 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `siliconflow/deepseek-r1-0528` | 2026-01-28 |  |
| 2 | `siliconflow/deepseek-v3-0324` | 2026-01-28 |  |
| 3 | `siliconflow/deepseek-v3.1-terminus` | 2026-01-28 |  |
| 4 | `siliconflow/deepseek-v3.2` | 2026-01-28 |  |

### vanchin (DeepSeek 第三方托管)（7 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `vanchin/deepseek-r1` | 2026-10-14 |  |
| 2 | `vanchin/deepseek-v3` | 2026-10-14 |  |
| 3 | `vanchin/deepseek-v3.1-terminus` | 2026-10-14 |  |
| 4 | `vanchin/deepseek-v3.2-think` | 2026-10-14 |  |
| 5 | `vanchin/deepseek-v4-pro` | 2026-08-28 |  |
| 6 | `vanchin/deepseek-v4-pro-0813` | 2026-08-28 |  |
| 7 | `vanchin/deepseek-v4.1-flash` | 2026-09-21 |  |

### StepFun 阶跃（2 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `stepfun/step-3.7-flash` | 2026-08-28 |  |
| 2 | `stepfun/step-5-preview` | 2026-09-21 |  |

### Xiaomi MiMo（1 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `xiaomi/mimo-v2.6-pro-ultraspeed` | 2026-10-19 |  |

### 其他对话模型（7 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `MiniMax-M2.5` | 2026-07-02 |  |
| 2 | `MiniMax/speech-02-hd` | 2026-10-14 |  |
| 3 | `MiniMax/speech-02-turbo` | 2026-10-14 |  |
| 4 | `MiniMax/speech-2.8-hd` | 2026-10-14 |  |
| 5 | `MiniMax/speech-2.8-turbo` | 2026-10-14 |  |
| 6 | `fun-asr-flash-2026-06-15` | 2026-06-17 |  |
| 7 | `unisound/unisound-u2` | 2026-08-28 |  |

### Qwen 千问（图像/语音/Omni/向量）（40 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `qwen-audio-3.0-asr-flash` | 2026-07-30 |  |
| 2 | `qwen-audio-3.0-realtime-flash` | 2026-09-14 |  |
| 3 | `qwen-audio-3.0-realtime-plus` | 2026-09-16 |  |
| 4 | `qwen-audio-3.1-realtime-plus` | 2026-10-12 |  |
| 5 | `qwen-image-2.0` | 2026-03-03 |  |
| 6 | `qwen-image-2.0-2026-03-03` | 2026-03-03 |  |
| 7 | `qwen-image-2.0-pro` | 2026-03-03 |  |
| 8 | `qwen-image-2.0-pro-2026-03-03` | 2026-03-03 |  |
| 9 | `qwen-image-2.0-pro-2026-04-22` | 2026-04-23 |  |
| 10 | `qwen-image-2.0-pro-2026-06-22` | 2026-07-02 |  |
| 11 | `qwen-image-2.1-pro` | 2026-10-03 |  |
| 12 | `qwen-image-3.0` | 2026-09-24 |  |
| 13 | `qwen-image-3.0-pro` | 2026-09-28 |  |
| 14 | `qwen-vl-ocr` | 2025-07-31 |  |
| 15 | `qwen-vl-ocr-2025-11-20` | 2026-07-16 |  |
| 16 | `qwen3-asr-flash-realtime` | 2026-10-14 |  |
| 17 | `qwen3-livetranslate-flash` | 2025-12-04 |  |
| 18 | `qwen3-livetranslate-flash-2025-12-01` | 2025-12-03 |  |
| 19 | `qwen3-omni-flash` | 2026-07-29 |  |
| 20 | `qwen3-omni-flash-realtime-2025-09-15` | 2026-10-13 |  |
| 21 | `qwen3-s2s-flash-realtime-2025-09-22` | 2026-10-13 |  |
| 22 | `qwen3-tts-flash` | 2026-10-14 |  |
| 23 | `qwen3-tts-flash-realtime` | 2026-10-14 |  |
| 24 | `qwen3.5-livetranslate-flash-realtime` | 2026-10-14 |  |
| 25 | `qwen3.5-livetranslate-flash-realtime-2026-05-19` | 2026-10-14 |  |
| 26 | `qwen3.5-ocr` | 2026-06-15 |  |
| 27 | `qwen3.5-omni-flash` | 2026-07-06 |  |
| 28 | `qwen3.5-omni-flash-2026-03-15` | 2026-07-06 |  |
| 29 | `qwen3.5-omni-flash-realtime` | 2026-04-16 |  |
| 30 | `qwen3.5-omni-flash-realtime-2026-03-15` | 2026-04-16 |  |
| 31 | `qwen3.5-omni-plus` | 2026-07-30 |  |
| 32 | `qwen3.5-omni-plus-2026-03-15` | 2026-09-29 |  |
| 33 | `qwen3.5-omni-plus-realtime` | 2026-04-16 |  |
| 34 | `qwen3.5-omni-plus-realtime-2026-03-15` | 2026-04-17 |  |
| 35 | `qwen3.7-text-embedding` | 2026-07-15 |  |
| 36 | `qwen3.7-text-embedding-flash` | 2026-08-31 |  |
| 37 | `qwen3.7-text-rerank` | 2026-09-04 |  |
| 38 | `qwen3.8-livetranslate-flash-realtime` | 2026-10-12 |  |
| 39 | `qwen3.8-omni-flash` | 2026-10-13 |  |
| 40 | `qwen3.8-omni-flash-realtime` | 2026-10-15 |  |

### Wan 万相 / 图像生成（3 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `wan2.7-image` | 2026-06-08 |  |
| 2 | `wan2.7-image-pro` | 2026-06-05 |  |
| 3 | `z-image-turbo` | 2025-12-19 |  |

### 其他多模态（GUI/图像）（1 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `gui-plus` | 2025-11-05 |  |

### 疑似内部/测试条目（5 个）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `cmaas-qwen3.8-flash` | 2026-10-13 |  |
| 2 | `sre-gpu-auto-handle` | 2026-06-10 |  |
| 3 | `test-sre-gpu-auto-handle` | 2026-08-21 |  |
| 4 | `tongyi-xiaomi-analysis-flash` | 2026-02-10 |  |
| 5 | `tongyi-xiaomi-analysis-pro` | 2026-02-10 |  |

## 三、DeepSeek 官方（2 条）

| # | 模型 ID | created (UTC) | 本地白名单 |
|---:|---|---|:---:|
| 1 | `deepseek-flash` |  | ✅ |
| 2 | `deepseek-v4-pro` |  | ✅ |

config.toml 给 `deepseek` 配了 3 个白名单模型，其中 `deepseek-flash` 不在上游返回里（配置里另有它的 `prices` 条目）。

## 四、关于「170」这个数字

上游实测 **178**，与 170 差 8。界面上少掉的那几条最可能来自：

1. **非文本对话模型被隐藏** — 图像 / TTS / ASR / Omni / realtime / embedding / rerank 共 44 条，远多于 8，所以不太可能是整类剔除。
2. **内部/测试条目被隐藏** — 5 条：`cmaas-qwen3.8-flash`、`sre-gpu-auto-handle`、`test-sre-gpu-auto-handle`、`tongyi-xiaomi-analysis-flash`、`tongyi-xiaomi-analysis-pro`，与 8 差 2。
3. **拉取时间差** — 网关模型目录会随时增删，你看到 170 的时刻与本次请求之间可能上线了新模型。

告诉我 170 具体是在哪个界面看到的（Settings > Model 的哪个 tab、有没有筛选开关），我按同一口径复现计数。

## 五、复现命令

```bash
cd ~/.reasonix && set -a && . ./.env && set +a
curl -s -H "Authorization: Bearer $ALI_WZQ_API_KEY" \
  https://maas.qianwenaiapi.com/compatible-mode/v1/models \
  | python3 -c 'import sys,json;print(len(json.load(sys.stdin)["data"]))'   # 178
curl -s -H "Authorization: Bearer $DEEPSEEK_API_KEY" https://api.deepseek.com/models \
  | python3 -c 'import sys,json;print(len(json.load(sys.stdin)["data"]))'   # 2
```

> 本文件不含任何密钥；上游 `/models` 返回体本身也不含密钥。
