import { Router } from "express";
import { getStats } from "../stats-store.js";

const router = Router();

router.get("/health", (_req, res) => {
  const stats = getStats();
  res.json({
    status: "ok",
    uptime_seconds: Math.floor(process.uptime()),
    total_requests: stats.total_requests,
  });
});

export default router;
