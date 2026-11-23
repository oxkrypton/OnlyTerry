// GitHub Pages 前端：把订阅表单提交到小机器的订阅 API。
// 部署前把下面的地址改成你的服务公网地址（与服务端 .env 的 PUBLIC_URL 一致，且已配置 PAGES_URL 放行本页面来源）。
const API_BASE = "https://api.terry.ronyu.me:8443";

const form = document.querySelector("#subscription-form");
const button = document.querySelector("#submit");
const result = document.querySelector("#result");

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  result.className = "result";
  const channels = [...form.querySelectorAll('input[name="channel"]:checked')].map((input) => input.value);
  if (channels.length === 0) {
    result.textContent = "请至少选择一个频道。";
    result.classList.add("error");
    return;
  }
  button.disabled = true;
  button.firstElementChild.textContent = "正在发送…";
  result.textContent = "";
  try {
    const response = await fetch(`${API_BASE}/api/subscriptions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: form.email.value.trim(), channels }),
    });
    const data = await response.json();
    result.textContent = data.message || "请求已完成。";
    result.classList.add(response.ok ? "success" : "error");
    if (response.ok) form.email.value = "";
  } catch {
    result.textContent = "网络连接失败，请稍后重试。";
    result.classList.add("error");
  } finally {
    button.disabled = false;
    button.firstElementChild.textContent = "发送确认邮件";
  }
});
