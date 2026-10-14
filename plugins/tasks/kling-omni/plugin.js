// yunai fork: Kling Omni video models on the legacy-format /v1/videos/omni-video API.
// The upstream "kling" plugin only drives text2video / image2video and AK|SK keys.
export const meta = {
  apiVersion: 1,
  key: "kling-omni",
  name: "Kling Omni",
  icon: "Kling.Color",
  description: {
    en: "Kuaishou Kling Omni video generation (Kling 3.0 Omni and Kling O1)",
    zh: "快手可灵全能视频生成（可灵 3.0 Omni、可灵 O1）",
  },
  version: "1.0.0",
  author: { name: "yunai" },
  baseUrl: "https://api-beijing.klingai.com",
  models: ["kling-v3-omni", "kling-video-o1"],
  fetchMode: "per_task",
  upstreams: ["vendor", "new_api"],
  usageSchema: {
    // Kling final unit deduction (estimated at submit, actual on completion).
    units: {
      type: "number",
      unit: "credit",
      description: { en: "Kling credit unit price", zh: "可灵资源包单位单价" },
    },
  },
  usageExamples: [
    { label: "v3-omni std 5s", facts: { units: 3 } },
    { label: "v3-omni pro · audio 5s", facts: { units: 5 } },
    { label: "v3-omni 4k 5s", facts: { units: 15 } },
    { label: "o1 std 5s", facts: { units: 3 } },
    { label: "o1 pro · video input 10s", facts: { units: 12 } },
  ],
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }, "openai_video"],
  routes: [
    { method: "POST", path: "/kling-omni/v1/videos/omni-video", type: "submit", action: "omni_video", decode: "decodeSubmit", render: "taskCreated" },
    { method: "GET", path: "/kling-omni/v1/videos/omni-video/:task_id", type: "query", render: "taskStatus" },
  ],
};

const OMNI_PATH = "/v1/videos/omni-video";

// Units per output second, not a currency price.
// Source: https://kling.ai/document-api/pricing/base/video.md (checked 2026-10-14).
// Legacy mode maps to resolution: std = 720p, pro = 1080p, 4k = 4K. "master" is
// not on the price page, so it reserves the model's highest rate; settlement
// always follows final_unit_deduction. Audio with a reference video is not
// listed either, so the higher of the two applicable rates is reserved.
const UNITS_PER_SECOND = {
  "kling-v3-omni": {
    std: { plain: 0.6, sound: 0.8, video: 0.9 },
    pro: { plain: 0.8, sound: 1.0, video: 1.2 },
    "4k": { plain: 3.0, sound: 3.0, video: 3.0 },
    master: { plain: 3.0, sound: 3.0, video: 3.0 },
  },
  "kling-video-o1": {
    std: { plain: 0.6, sound: 0.6, video: 0.9 },
    pro: { plain: 0.8, sound: 0.8, video: 1.2 },
    master: { plain: 1.2, sound: 1.2, video: 1.2 },
  },
};

// Duration bounds per model (seconds), from the Kling 3.0 Omni and O1 API pages.
// Decoders may run before channel mapping resolves an alias, so they check the
// widest range; the driver hooks re-check against the resolved model.
const DURATION_RANGE = {
  "kling-v3-omni": [3, 15],
  "kling-video-o1": [3, 10],
};
const ANY_MODEL_DURATION = [3, 15];

const MODES = ["std", "pro", "master", "4k"];
const ASPECT_RATIOS = ["16:9", "9:16", "1:1"];

function trimmed(value) {
  return String(value || "").trim();
}

function isObject(value) {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function omniModel(ctx, req) {
  const model = (ctx && ctx.upstreamModel) || (ctx && ctx.model) || (req && req.model) || "";
  if (!UNITS_PER_SECOND[model]) throw new Error("model must be one of " + Object.keys(UNITS_PER_SECOND).join(", "));
  return model;
}

function viaGateway(ctx) {
  return !!(ctx.upstream && ctx.upstream.kind === "new_api");
}

// Kling accepts a single API key as a Bearer token; the legacy accessKey|secretKey
// pair is still signed into a short-lived JWT.
function tokenFor(ctx) {
  const apiKey = trimmed(ctx.apiKey);
  if (viaGateway(ctx) || !apiKey.includes("|")) return apiKey;
  const parts = apiKey.split("|");
  if (parts.length !== 2) throw new Error("invalid api_key, required format is an API key or accessKey|secretKey");
  const now = utils.unixNow();
  return utils.jwtSignHS256({ iss: parts[0].trim(), exp: now + 1800, nbf: now - 5 }, parts[1].trim());
}

function omniURL(ctx) {
  return ctx.baseUrl + (viaGateway(ctx) ? "/kling-omni" : "") + OMNI_PATH;
}

function authHeaders(ctx) {
  return { Accept: "application/json", Authorization: "Bearer " + tokenFor(ctx) };
}

function aspectRatio(size) {
  const ratios = { "1024x1024": "1:1", "512x512": "1:1", "1280x720": "16:9", "1920x1080": "16:9", "720x1280": "9:16", "1080x1920": "9:16" };
  return ratios[trimmed(size)] || "";
}

function hasFirstFrame(vendor) {
  return (vendor.image_list || []).some(function (image) {
    return isObject(image) && image.type === "first_frame";
  });
}

function hasVideoInput(vendor) {
  return Array.isArray(vendor.video_list) && vendor.video_list.length > 0;
}

function hasReferenceInput(vendor) {
  const referImage = (vendor.image_list || []).some(function (image) {
    return isObject(image) && image.type !== "first_frame" && image.type !== "end_frame";
  });
  return referImage || hasVideoInput(vendor) || (Array.isArray(vendor.element_list) && vendor.element_list.length > 0);
}

function hasBaseVideo(vendor) {
  return (vendor.video_list || []).some(function (video) {
    return isObject(video) && video.refer_type === "base";
  });
}

// normalizeOmniBody validates a legacy omni-video body and fills the fields the
// estimate depends on (duration, mode, sound), so the reserved units and the
// request sent upstream always describe the same video. An unresolved model
// (a channel alias seen before mapping) gets only the model-independent checks.
function normalizeOmniBody(model, source) {
  if (!isObject(source)) throw new Error("request body must be an object");
  const vendor = Object.assign({}, source);
  delete vendor.model;
  delete vendor.model_name;

  if (typeof vendor.prompt !== "string" || !vendor.prompt.trim()) throw new Error("prompt is required");
  for (const name of ["image_list", "video_list", "element_list"]) {
    if (vendor[name] === undefined) continue;
    if (!Array.isArray(vendor[name]) || !vendor[name].every(isObject)) throw new Error(name + " must be an array of objects");
  }

  const range = DURATION_RANGE[model] || ANY_MODEL_DURATION;
  const duration = vendor.duration === undefined || vendor.duration === "" ? 5 : Number(vendor.duration);
  if (!Number.isInteger(duration) || duration < range[0] || duration > range[1])
    throw new Error("duration must be an integer from " + range[0] + " to " + range[1] + " seconds for " + model);
  // O1 makes only 5 or 10 second videos from text or start/end frames alone; other
  // lengths need a reference image, video or element (Kling code 1201).
  if (model === "kling-video-o1" && !hasReferenceInput(vendor) && duration !== 5 && duration !== 10)
    throw new Error("duration must be 5 or 10 seconds for kling-video-o1 without a reference image, video or element");
  vendor.duration = String(duration);

  const mode = trimmed(vendor.mode).toLowerCase() || "std";
  const modes = UNITS_PER_SECOND[model] ? Object.keys(UNITS_PER_SECOND[model]) : MODES;
  if (!modes.includes(mode)) throw new Error("mode must be one of " + modes.join(", ") + (UNITS_PER_SECOND[model] ? " for " + model : ""));
  vendor.mode = mode;

  const sound = trimmed(vendor.sound).toLowerCase() || "off";
  if (sound !== "on" && sound !== "off") throw new Error("sound must be on or off");
  vendor.sound = sound;

  if (vendor.aspect_ratio !== undefined && !ASPECT_RATIOS.includes(vendor.aspect_ratio)) throw new Error("aspect_ratio must be one of " + ASPECT_RATIOS.join(", "));
  if (vendor.aspect_ratio === undefined && !hasFirstFrame(vendor) && !hasBaseVideo(vendor)) vendor.aspect_ratio = "16:9";
  return vendor;
}

function estimateUnits(model, vendor) {
  const rates = UNITS_PER_SECOND[model][vendor.mode];
  let rate = rates.plain;
  if (vendor.sound === "on") rate = Math.max(rate, rates.sound);
  if (hasVideoInput(vendor)) rate = Math.max(rate, rates.video);
  // Rounded so 0.6 * 3 is logged as 1.8 rather than 1.7999999999999998.
  return Math.round(rate * Number(vendor.duration) * 1000) / 1000;
}

function filePlaceholder(image) {
  if (!isObject(image) || !image.__fileRef) return image;
  const placeholder = { __fileRef: image.__fileRef, encoding: image.encoding };
  if (image.mimeType) placeholder.mimeType = image.mimeType;
  if (image.maxBytes !== undefined && image.maxBytes !== null) placeholder.maxBytes = image.maxBytes;
  return placeholder;
}

// omniRequest is the normalized submit intent shared by every entry surface.
function omniRequest(ctx, model, vendorSource) {
  const vendor = normalizeOmniBody((ctx && ctx.upstreamModel) || model, vendorSource);
  return { kind: "submit", model: model, action: "omni_video", requestBody: { model: model, vendor: vendor } };
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
      if (!isObject(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") {
          texts.push(part);
          continue;
        }
        if (!isObject(part)) continue;
        if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
        if (["input_image", "image_url"].includes(part.type)) {
          let image = part.image_url;
          if (isObject(image)) image = image.url;
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

// Frames from the OpenAI-style surfaces: the first image is the first frame, a
// second one the end frame, matching the legacy Kling image_tail convention.
function framesFrom(images) {
  return images.slice(0, 2).map(function (image, index) {
    return { image_url: image, type: index === 0 ? "first_frame" : "end_frame" };
  });
}

function decodeNativeSubmit(ctx) {
  if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
  const body = ctx.body.value;
  if (!isObject(body)) throw new Error("request body must be an object");
  const model = trimmed(typeof body.model_name === "string" && body.model_name ? body.model_name : body.model);
  if (!model) throw new Error("model is required");
  return omniRequest(ctx, model, body);
}

export const native = {
  decodeSubmit: decodeNativeSubmit,
  taskCreated: function (ctx, task) {
    const result = task.data || {},
      data = result.data || {};
    return Object.assign({}, result, { data: Object.assign({}, data, { task_id: task.task_id }) });
  },
  taskStatus: function (ctx, task) {
    if (isObject(task.data)) {
      const result = task.data,
        data = isObject(result.data) ? result.data : {};
      return Object.assign({}, result, { data: Object.assign({}, data, { task_id: task.task_id }) });
    }
    const statusMap = { NOT_START: "submitted", SUBMITTED: "submitted", QUEUED: "submitted", IN_PROGRESS: "processing", SUCCESS: "succeed", FAILURE: "failed" };
    return { code: 0, data: { task_id: task.task_id, task_status: statusMap[task.status] || "submitted", task_status_msg: task.fail_reason || "" } };
  },
  error: function (ctx, error) {
    return { code: error.code, message: error.message };
  },
};

export function buildSubmitRequest(ctx) {
  const req = ctx.requestBody || {};
  const model = omniModel(ctx, req);
  const vendor = normalizeOmniBody(model, req.vendor);
  vendor.image_list = (vendor.image_list || []).map(function (image) {
    return Object.assign({}, image, { image_url: filePlaceholder(image.image_url) });
  });
  if (!vendor.image_list.length) delete vendor.image_list;
  return {
    url: omniURL(ctx),
    method: "POST",
    headers: Object.assign({ "Content-Type": "application/json" }, authHeaders(ctx)),
    body: Object.assign(vendor, { model_name: model }),
    action: "omni_video",
  };
}

export function parseSubmitResponse(ctx, resp) {
  const result = resp.body || {};
  if (result.code !== 0) throw new Error(result.message || "kling submit failed");
  if (!result.data || !result.data.task_id) throw new Error("missing task_id");
  return { taskId: result.data.task_id, taskData: result };
}

export function extractUsage(ctx) {
  if (ctx.usagePurpose === "billing_ratios") return null;
  const req = ctx.requestBody || {};
  const model = omniModel(ctx, req);
  return { units: estimateUnits(model, normalizeOmniBody(model, req.vendor)) };
}

export function buildQueryRequest(ctx) {
  return { url: omniURL(ctx) + "/" + ctx.taskId, method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(ctx, body) {
  const data = (body && body.data) || {};
  const statuses = { submitted: "SUBMITTED", processing: "IN_PROGRESS", succeed: "SUCCESS", failed: "FAILURE" };
  const status = statuses[data.task_status];
  if (!status) return { status: "UNKNOWN", reason: "unknown task status: " + String(data.task_status || "") };
  const videos = status === "SUCCESS" && data.task_result && data.task_result.videos ? data.task_result.videos : [];
  const result = { code: body.code || 0, taskId: data.task_id, status: status, reason: data.task_status_msg || "" };
  if (videos.length && videos[0].url) result.url = videos[0].url;
  // Ratio-priced settlement reads the deducted units as tokens, as in the kling plugin.
  const units = Number.parseFloat(data.final_unit_deduction || "");
  if (Number.isFinite(units) && units > 0) {
    result.completionTokens = Math.ceil(units);
    result.totalTokens = Math.ceil(units);
  }
  return result;
}

function artifactData(ctx) {
  const data = (ctx && ctx.data) || {};
  if (isObject(data.data) && data.data.task_id && Object.prototype.hasOwnProperty.call(data.data, "data")) return data.data.data || {};
  return data;
}

function artifactVideoURL(ctx) {
  const result = (artifactData(ctx).data || {}).task_result || {};
  const videos = Array.isArray(result.videos) ? result.videos : [];
  return videos.length ? String(videos[0].url || "").trim() : "";
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && artifactVideoURL(task) ? [{ key: "video", type: "video" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const url = artifactVideoURL(ctx);
  if (!url) throw new Error("artifact_not_found");
  return { url: url, method: ctx.clientRequest.method, credentialless: true };
}

// Settlement follows the units Kling actually deducted for the finished task.
export function extractUsageOnComplete(_task, _taskResult, body) {
  const data = (body && body.data) || {};
  const raw = data.final_unit_deduction;
  if (raw === undefined || raw === null || raw === "") return null;
  const units = Number.parseFloat(raw);
  if (!Number.isFinite(units) || units < 0) return null;
  return { units: units };
}

export const protocols = {
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!isObject(req)) throw new Error("request body must be an object");
      const model = trimmed(ctx.model);
      if (!model) throw new Error("model is required");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      if (req.images !== undefined && !Array.isArray(req.images)) throw new Error("images must be an array");
      if (req.metadata !== undefined && !isObject(req.metadata)) throw new Error("metadata must be an object");
      const input = responsesInput(req);
      const images = [];
      for (const image of [req.image, req.input_reference].concat(req.images || [], input.images)) {
        if (trimmed(image) && !images.includes(trimmed(image))) images.push(trimmed(image));
      }
      const vendor = Object.assign({ prompt: input.prompt || trimmed(req.prompt) }, req.metadata || {});
      if (images.length && vendor.image_list === undefined) vendor.image_list = framesFrom(images);
      for (const name of ["mode", "sound", "aspect_ratio"]) {
        if (Object.prototype.hasOwnProperty.call(req, name)) vendor[name] = req[name];
      }
      if (Object.prototype.hasOwnProperty.call(req, "seconds")) vendor.duration = req.seconds;
      else if (Object.prototype.hasOwnProperty.call(req, "duration")) vendor.duration = req.duration;
      if (vendor.aspect_ratio === undefined && aspectRatio(req.size)) vendor.aspect_ratio = aspectRatio(req.size);
      return omniRequest(ctx, model, vendor);
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
      if (status === "FAILURE") return { events: [{ type: "error", code: "task_failed", message: task.fail_reason || "task failed" }], state: state, done: true };
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
        metadata: { vendor: "kling" },
      };
    },
  },
  openai_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
      let req;
      let referenceFile = false;
      if (ctx.body.kind === "json") {
        if (!isObject(ctx.body.value)) throw new Error("JSON object required");
        req = Object.assign({}, ctx.body.value);
      } else {
        req = {};
        const fields = ctx.body.fields || {};
        for (const name of Object.keys(fields)) {
          if (fields[name].length > 1) throw new Error(name + " must be provided once");
          req[name] = fields[name][0];
        }
        for (const file of ctx.body.files || []) {
          if (file.field !== "input_reference") throw new Error("unexpected file field: " + file.field);
          if (referenceFile) throw new Error("input_reference must be provided once");
          referenceFile = true;
        }
        if (req.metadata !== undefined) {
          let parsed;
          try {
            parsed = JSON.parse(req.metadata);
          } catch (e) {
            throw new Error("metadata must be a JSON object string");
          }
          if (!isObject(parsed)) throw new Error("metadata must be a JSON object string");
          req.metadata = parsed;
        }
      }
      if (req.metadata !== undefined && !isObject(req.metadata)) throw new Error("metadata must be an object");
      const vendor = Object.assign({ prompt: typeof req.prompt === "string" ? req.prompt : "" }, req.metadata || {});
      const image = referenceFile ? { __fileRef: "request_file:input_reference", encoding: "base64", maxBytes: 10485760 } : trimmed(req.input_reference || req.image);
      if (image && vendor.image_list === undefined) vendor.image_list = [{ image_url: image, type: "first_frame" }];
      for (const name of ["mode", "sound", "aspect_ratio"]) {
        if (req[name] !== undefined) vendor[name] = req[name];
      }
      if (req.seconds !== undefined) vendor.duration = Number(req.seconds);
      else if (req.duration !== undefined) vendor.duration = Number(req.duration);
      if (vendor.aspect_ratio === undefined && aspectRatio(req.size)) vendor.aspect_ratio = aspectRatio(req.size);
      return omniRequest(ctx, ctx.model, vendor);
    },
    render: function (ctx, task) {
      const response = task.data || {};
      const data = response.data || {};
      const statusMap = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
      const output = {
        id: task.task_id,
        object: "video",
        model: "",
        status: statusMap[task.status] || "unknown",
        progress: Number(String(task.progress || "0").replace("%", "")),
        created_at: data.created_at || 0,
      };
      if (data.updated_at) output.completed_at = data.updated_at;
      const videos = data.task_result && data.task_result.videos ? data.task_result.videos : [];
      if (videos.length && videos[0].duration) output.seconds = videos[0].duration;
      if (response.code !== 0 && response.message) output.error = { message: response.message, code: String(response.code) };
      if (data.task_status === "failed") output.error = { message: data.task_status_msg, code: "" };
      return output;
    },
  },
};
