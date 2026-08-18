import { Router } from "express";
import { getStats, getRequests, resetStats } from "../stats-store.js";

const router = Router();

router.get("/stats", (_req, res) => {
  res.json(getStats());
});

router.get("/stats/requests", (req, res) => {
  const limit = parseInt(req.query.limit as string) || 50;
  const offset = parseInt(req.query.offset as string) || 0;
  const model = req.query.model as string | undefined;

  res.json(getRequests({ limit, offset, model }));
});

router.delete("/stats", (_req, res) => {
  resetStats();
  res.json({ success: true });
});

export default router;
