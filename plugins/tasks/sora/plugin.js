export const meta = {
  apiVersion: 1,
  key: "sora",
  name: "Sora",
  icon: "Sora.Color",
  description: {
    en: "OpenAI Sora video generation (text-to-video, image-to-video, and remix)",
    zh: "OpenAI Sora 视频生成（文生视频、图生视频、remix）",
  },
  version: "1.1.2",
  channelTypes: [55, 1], // OpenAI-type channels natively serve sora with the same wire format
  author: { name: "QuantumNous" },
  models: ["sora-2", "sora-2-pro"],
  fetchMode: "per_task",
  // A New API gateway serves /v1/videos as a host protocol, so no URL changes.
  upstreams: ["vendor", "new_api"],
  usageSchema: {
    // Requested video duration in seconds.
    seconds: {
      type: "number",
      unit: "second",
      description: { en: "Video generation unit price", zh: "视频生成单价" },
    },
    // Requested output video dimensions.
    size: {
      enum: ["720x1280", "1280x720", "1792x1024", "1024x1792"],
      enumLabels: {
        "720x1280": { en: "720x1280", zh: "720x1280" },
        "1280x720": { en: "1280x720", zh: "1280x720" },
        "1792x1024": { en: "1792x1024", zh: "1792x1024" },
        "1024x1792": { en: "1024x1792", zh: "1024x1792" },
      },
      description: { en: "Output video dimensions", zh: "输出视频尺寸" },
    },
  },
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
};

function trimmed(value) {
  return String(value || "").trim();
}

function hasVideoInput(req, metadata) {
  const content = Array.isArray(metadata.content) ? metadata.content : Array.isArray(req.content) ? req.content : [];
  for (const item of content) {
    if (item && (item.type === "video_url" || Object.prototype.hasOwnProperty.call(item, "video_url"))) return true;
  }
  if (req.video_url || metadata.video_url || req.video || metadata.video) return true;
  const refs = [].concat(req.images || [], req.image ? [req.image] : [], req.input_reference ? [req.input_reference] : []);
  for (const ref of refs) {
    if (typeof ref === "string" && /\.(mp4|mov|webm|avi|mkv)(\?|$)/i.test(ref)) return true;
  }
  return false;
}

function maxTokensPerSecond(resolution) {
  if (resolution === "480p") return 9720;
  if (resolution === "1080p") return 48960;
  if (resolution === "4k") return 194400;
  return 21600;
}

function responsesInput(req) {
  const texts = [],
    images = [];
  const input = req.input;
  if (typeof input === "string") texts.push(input);
  else if (Array.isArray(input)) {
    for (const item of input) {
      if (typeof item === "string") {
        texts.push(item);
        continue;
      }
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") {
          texts.push(part);
          continue;
        }
        if (!part || typeof part !== "object" || Array.isArray(part)) continue;
        if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
        if (["input_image", "image_url"].includes(part.type)) {
          let image = part.image_url;
          if (image && typeof image === "object") image = image.url;
          if (trimmed(image)) images.push(trimmed(image));
        }
      }
    }
  }
  return {
    prompt: texts
      .filter(function (text) {
        return trimmed(text);
      })
      .join("\n"),
    images: images,
  };
}

function responsesVideoText(ctx) {
  const artifact = ctx && ctx.artifacts && ctx.artifacts.video;
  const url = trimmed(artifact && artifact.url);
  if (!url) throw new Error("video artifact is unavailable");
  const escaped = url.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return '<video controls src="' + escaped + '"></video>';
}

function requestValues(req, model) {
  const values = Object.assign({}, req || {});
  values.model = model;
  return values;
}

export function buildSubmitRequest(ctx) {
  const req = ctx.requestBody || {};
  if (!String(req.prompt || "").trim()) throw new Error("field prompt is required");
  const action = ctx.action === "remix" ? "remix" : ctx.action;
  const headers = { Authorization: "Bearer " + ctx.apiKey };
  if (action === "remix") {
    headers["Content-Type"] = "application/json";
    return { url: ctx.baseUrl + "/v1/videos/" + ctx.originTaskId + "/remix", method: "POST", headers, body: requestValues(req, ctx.upstreamModel), action };
  }
  if ((ctx.files || []).length) {
    const parts = [];
    const values = requestValues(req, ctx.upstreamModel);
    for (const key of Object.keys(values)) {
      if (values[key] !== undefined && values[key] !== null && typeof values[key] !== "object") parts.push({ name: key, value: values[key] });
    }
    if (values.metadata && typeof values.metadata === "object" && !Array.isArray(values.metadata)) {
      parts.push({ name: "metadata", value: JSON.stringify(values.metadata) });
    }
    for (const file of ctx.files) parts.push({ name: file.field, fileRef: file.ref, filename: file.filename });
    return { url: ctx.baseUrl + "/v1/videos", method: "POST", headers, bodyType: "multipart", parts };
  }
  headers["Content-Type"] = "application/json";
  return { url: ctx.baseUrl + "/v1/videos", method: "POST", headers, body: requestValues(req, ctx.upstreamModel) };
}

export function parseSubmitResponse(ctx, resp) {
  const body = resp.body || {};
  const taskId = body.id || body.task_id;
  if (!taskId) throw new Error("task_id is empty");
  return { taskId, taskData: body };
}

export function extractUsage(ctx) {
  if (ctx.action === "remix") return {};
  const req = ctx.requestBody || {};
  const metadata =
    (req.vendorBody && typeof req.vendorBody === "object" ? req.vendorBody : null) ||
    (req.metadata && typeof req.metadata === "object" ? req.metadata : {});
  let seconds = Number(req.seconds || req.duration || metadata.duration || 4);
  if (!Number.isFinite(seconds) || seconds <= 0) seconds = 4;
  const rawSize = trimmed(req.size);
  const size = ["720x1280", "1280x720", "1792x1024", "1024x1792"].includes(rawSize) ? rawSize : "720x1280";
  const facts = { seconds: Math.min(seconds, 3600), size: size };
  const modelName = trimmed((ctx && (ctx.upstreamModel || ctx.model)) || req.model).toLowerCase();
  if (modelName.includes("seedance") || metadata.resolution || req.resolution || metadata.content || req.content) {
    const rawRes = trimmed(metadata.resolution || req.resolution || req.size).toLowerCase();
    if (["480p", "720p", "1080p", "4k"].includes(rawRes)) facts.resolution = rawRes;
    facts.video_input = hasVideoInput(req, metadata) ? "video" : "none";
    if (typeof metadata.generate_audio === "boolean") facts.generate_audio = metadata.generate_audio;
  }
  return facts;
}

export function extractUsageOnComplete(task, taskResult, body) {
  const facts = {};
  const seconds = Number((body || {}).seconds || (body || {}).duration || 0);
  if (Number.isFinite(seconds) && seconds > 0) facts.seconds = Math.min(seconds, 3600);
  const size = trimmed((body || {}).size);
  if (["720x1280", "1280x720", "1792x1024", "1024x1792"].includes(size)) facts.size = size;
  const state = taskResult && taskResult.plugin_state && typeof taskResult.plugin_state === "object" ? taskResult.plugin_state : {};
  let tokens = Number(state.tokens || 0);
  if (!Number.isFinite(tokens) || tokens <= 0) {
    tokens = Number((taskResult && (taskResult.completion_tokens || taskResult.total_tokens)) || 0);
  }
  if (Number.isFinite(tokens) && tokens > 0) facts.tokens = tokens;
  const res = trimmed(state.resolution).toLowerCase();
  if (["480p", "720p", "1080p", "4k"].includes(res)) facts.resolution = res;
  const dur = Number(state.seconds || 0);
  if (Number.isFinite(dur) && dur > 0) facts.seconds = Math.min(dur, 3600);
  if (typeof state.generate_audio === "boolean") facts.generate_audio = state.generate_audio;
  if (["video", "none"].includes(trimmed(state.video_input).toLowerCase())) {
    facts.video_input = trimmed(state.video_input).toLowerCase();
  }
  return facts;
}

export function buildQueryRequest(ctx) {
  return { url: ctx.baseUrl + "/v1/videos/" + ctx.taskId, method: "GET", headers: { Authorization: "Bearer " + ctx.apiKey } };
}

export function parseTaskResult(ctx, body) {
  const statuses = {
    queued: "QUEUED",
    pending: "QUEUED",
    processing: "IN_PROGRESS",
    in_progress: "IN_PROGRESS",
    completed: "SUCCESS",
    failed: "FAILURE",
    cancelled: "FAILURE",
  };
  const mapped = statuses[body.status];
  const result = { status: mapped || "UNKNOWN" };
  if (!mapped) result.reason = "unrecognized status: " + String(body.status || "");
  if (body.progress > 0 && body.progress < 100) result.progress = body.progress + "%";
  if (result.status === "FAILURE") result.reason = body.error && body.error.message ? body.error.message : "task failed";
  // Compatible providers may return a direct video URL in metadata.
  // Keep Sora's content-proxy fallback when no HTTPS URL is provided.
  if (result.status === "SUCCESS" && body.metadata &&
      typeof body.metadata.url === "string" && /^https:\/\/[^/\s]+\//.test(body.metadata.url)) {
    result.url = body.metadata.url;
    if (ctx && ctx.baseUrl && ctx.taskId && ctx.apiKey && typeof utils !== "undefined" && utils && typeof utils.fetch === "function") {
      try {
        const extra = utils.fetch({
          url: ctx.baseUrl + "/v1/video/generations/" + encodeURIComponent(ctx.taskId),
          method: "GET",
          headers: { Authorization: "Bearer " + ctx.apiKey },
        });
        if (extra && extra.status === 200 && extra.body && typeof extra.body === "object") {
          const outer = extra.body.data && typeof extra.body.data === "object" ? extra.body.data : extra.body;
          const inner = outer.data && typeof outer.data === "object" ? outer.data : outer;
          const usage = inner.usage || outer.usage || body.usage || {};
          const completionTokens = Number(usage.completion_tokens || 0);
          const totalTokens = Number(usage.total_tokens || completionTokens || 0);
          const tokens = completionTokens > 0 ? completionTokens : totalTokens;
          if (Number.isFinite(completionTokens) && completionTokens > 0) result.completionTokens = completionTokens;
          if (Number.isFinite(totalTokens) && totalTokens > 0) result.totalTokens = totalTokens;
          const state = {};
          if (Number.isFinite(tokens) && tokens > 0) state.tokens = tokens;
          const content = inner.content && typeof inner.content === "object" ? inner.content : {};
          const res = trimmed(content.resolution || inner.resolution).toLowerCase();
          if (["480p", "720p", "1080p", "4k"].includes(res)) state.resolution = res;
          const dur = Number(inner.duration || outer.duration || 0);
          if (Number.isFinite(dur) && dur > 0) state.seconds = Math.min(dur, 3600);
          if (typeof inner.generate_audio === "boolean") state.generate_audio = inner.generate_audio;
          if (Number.isFinite(tokens) && tokens > 0 && Number.isFinite(dur) && dur > 0 && state.resolution) {
            const maxNoVideoTokens = dur * maxTokensPerSecond(state.resolution) * 1.15;
            state.video_input = tokens > maxNoVideoTokens ? "video" : "none";
          }
          if (Object.keys(state).length > 0) result.state = state;
        }
      } catch (e) {
        // Keep direct URL if supplementary usage fetch fails.
      }
    }
  }
  return result;
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  return {
    url: ctx.baseUrl + "/v1/videos/" + encodeURIComponent(ctx.upstreamTaskId) + "/content",
    method: ctx.clientRequest.method,
    headers: { Authorization: "Bearer " + ctx.apiKey },
  };
}

export const protocols = {
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
      const model = trimmed(req.model);
      if (!model) throw new Error("model is required");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      if (req.images !== undefined && !Array.isArray(req.images)) throw new Error("images must be an array");
      if (req.metadata !== undefined && (!req.metadata || typeof req.metadata !== "object" || Array.isArray(req.metadata)))
        throw new Error("metadata must be an object");
      const input = responsesInput(req);
      const prompt = input.prompt || trimmed(req.prompt);
      if (!prompt) throw new Error("input is required");
      const images = [];
      for (const image of [req.image, req.input_reference].concat(req.images || [], input.images)) {
        if (trimmed(image) && !images.includes(trimmed(image))) images.push(trimmed(image));
      }
      const requestBody = { model: model, prompt: prompt };
      if (images.length) requestBody.input_reference = images[0];
      if (Object.prototype.hasOwnProperty.call(req, "seconds")) requestBody.seconds = req.seconds;
      else if (Object.prototype.hasOwnProperty.call(req, "duration")) requestBody.seconds = req.duration;
      if (Object.prototype.hasOwnProperty.call(req, "size")) requestBody.size = req.size;
      if (Object.prototype.hasOwnProperty.call(req, "metadata")) requestBody.metadata = req.metadata;
      return { kind: "submit", model: model, action: images.length ? "image_to_video" : "text_to_video", requestBody: requestBody };
    },
    renderEvents: function (ctx, task, previousState) {
      const status = String(task.status || "UNKNOWN").toUpperCase();
      const value = Number(String(task.progress || "").replace("%", ""));
      const progress = Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
      const state = { status: status, progress: progress };
      if (status === "SUCCESS") {
        const text = responsesVideoText(ctx);
        const events = previousState && previousState.status === status ? [] : [{ type: "output", data: text }];
        return { events: events, state: state, done: true };
      }
      if (status === "FAILURE")
        return { events: [{ type: "error", code: "task_failed", message: task.fail_reason || "task failed" }], state: state, done: true };
      if (previousState && previousState.status === status && previousState.progress === progress) return { events: [], state: state, done: false };
      const event = { type: "progress", message: status.toLowerCase() };
      if (progress !== null) event.progress = progress;
      return { events: [event], state: state, done: false };
    },
    renderFinal: function (ctx, _task) {
      return {
        output: [
          {
            type: "message",
            status: "completed",
            role: "assistant",
            content: [{ type: "output_text", text: responsesVideoText(ctx), annotations: [], logprobs: [] }],
          },
        ],
        metadata: { vendor: "sora" },
      };
    },
  },
};

const legacyRenderers = {
  openai_video: function (task) {
    const statuses = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
    const output = {
      id: task.task_id,
      object: "video",
      model: (task.properties || {}).origin_model_name || "",
      status: statuses[task.status] || "unknown",
      progress: Number(String(task.progress || "0").replace("%", "")),
      created_at: Number(task.created_at || 0),
    };
    const completedAt = Number(task.finished_at || task.updated_at || 0);
    if (completedAt > 0) output.completed_at = completedAt;
    if (task.status === "FAILURE") {
      output.error = { code: "video_generation_failed", message: "The video generation task failed." };
    }
    return output;
  },
};

protocols.openai_video = {
  decodeRequest: function (ctx) {
    if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
    if (ctx.body.kind === "json") {
      if (!ctx.body.value || Array.isArray(ctx.body.value)) throw new Error("JSON object required");
      const req = ctx.body.value;
      const seconds = req.seconds === undefined ? req.duration : req.seconds;
      if (seconds !== undefined && (!Number.isFinite(Number(seconds)) || Number(seconds) <= 0 || Number(seconds) > 3600))
        throw new Error("seconds must be between 1 and 3600");
      return {
        kind: "submit",
        model: ctx.model,
        action: req.input_reference || req.image ? "image_to_video" : "text_to_video",
        requestBody: Object.assign({}, req, { model: ctx.model }),
      };
    }
    const first = function (name) {
      const values = (ctx.body.fields || {})[name] || [];
      if (values.length > 1) throw new Error(name + " must be provided once");
      return values[0];
    };
    const req = {};
    const fields = ctx.body.fields || {};
    for (const name of Object.keys(fields)) {
      req[name] = first(name);
    }
    let hasInputReferenceFile = false;
    for (const file of ctx.body.files || []) {
      if (file.field !== "input_reference") throw new Error("unexpected file field: " + file.field);
      if (hasInputReferenceFile) throw new Error("input_reference must be provided once");
      hasInputReferenceFile = true;
    }
    if (req.metadata !== undefined) {
      let parsed;
      try {
        parsed = JSON.parse(req.metadata);
      } catch (e) {
        throw new Error("metadata must be a JSON object string");
      }
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error("metadata must be a JSON object string");
      req.metadata = parsed;
    }
    if (req.seconds !== undefined) req.seconds = Number(req.seconds);
    else if (req.duration !== undefined) req.seconds = Number(req.duration);
    const seconds = req.seconds === undefined ? req.duration : req.seconds;
    if (seconds !== undefined && (!Number.isFinite(Number(seconds)) || Number(seconds) <= 0 || Number(seconds) > 3600))
      throw new Error("seconds must be between 1 and 3600");
    return {
      kind: "submit",
      model: ctx.model,
      action: hasInputReferenceFile || req.input_reference || req.image ? "image_to_video" : "text_to_video",
      requestBody: Object.assign({}, req, { model: ctx.model }),
    };
  },
  render: function (ctx, task) {
    if (task.data && typeof task.data === "object" && !Array.isArray(task.data)) return task.data;
    return legacyRenderers.openai_video(task);
  },
};
