import math
import time
import random
import logging
import json
import csv
from datetime import datetime
from typing import List, Optional, Dict, Any

# --- CONFIGURATION DES LOGS ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s', datefmt='%Y-%m-%d %H:%M:%S')

# --- STRUCTURES DE DONNÉES (Inspirées par votre code) ---
class Deployment:
    """Représente un déploiement, inspiré de votre classe pour plus de clarté."""
    def __init__(self, id: str, min_replicas: int, max_replicas: int, poids_ratio: float = 1.0, groupe_id: Optional[str] = None):
        self.id = id
        self.min = min_replicas
        self.max = max_replicas
        self.poids_ratio = poids_ratio
        self.groupe_id = groupe_id
        # L'état actuel est géré par la boucle de simulation, pas par la classe elle-même
        self.replicas_actuels = self.max

    @property
    def max_possible_reduction(self) -> int:
        """La marge maximale de réduction possible depuis l'état optimal."""
        return self.max - self.min

# --- GESTIONNAIRE DE GRAPHIQUE ---
try:
    import matplotlib.pyplot as plt
    MATPLOTLIB_AVAILABLE = True
except ImportError:
    logging.warning("Matplotlib non trouvé. Installez-le avec : pip install matplotlib")
    MATPLOTLIB_AVAILABLE = False
class RealTimePlotter: # ... (Code identique)
    def __init__(self, deployment_ids: List[str]):
        if not MATPLOTLIB_AVAILABLE: return
        self.fig, (self.ax1, self.ax2) = plt.subplots(2, 1, figsize=(12, 9), sharex=True)
        plt.ion()
        self.timestamps, self.optimal_power_data, self.current_power_data, self.total_replicas_data = [], [], [], []
        self.individual_replicas_data = {dep_id: [] for dep_id in deployment_ids}
        self.ax1.set_title("Évolution de la Puissance Électrique et du Total des Réplicas")
        self.ax1.set_ylabel("Puissance (kW)", color='tab:blue')
        self.line_optimal_power, = self.ax1.plot([], [], 'k--', label="Puissance Optimale")
        self.line_current_power, = self.ax1.plot([], [], 'b-', label="Puissance Actuelle", lw=2)
        self.ax1b = self.ax1.twinx()
        self.ax1b.set_ylabel("Total Réplicas", color='tab:red')
        self.line_total_replicas, = self.ax1b.plot([], [], 'r-', label="Total Réplicas")
        self.fig.legend(loc="upper center", bbox_to_anchor=(0.5, 1.0), ncol=3)
        self.ax1.grid(True)
        self.ax2.set_title("Évolution des Réplicas par Déploiement")
        self.ax2.set_xlabel("Cycle de Simulation")
        self.ax2.set_ylabel("Nombre de Réplicas")
        self.individual_lines = {dep_id: self.ax2.plot([], [], label=dep_id)[0] for dep_id in deployment_ids}
        self.ax2.legend(loc="upper left", bbox_to_anchor=(1.05, 1.0))
        self.fig.tight_layout(rect=[0, 0, 0.85, 0.95])
    def update(self, cycle, optimal_power, current_power, total_replicas, individual_replicas):
        if not MATPLOTLIB_AVAILABLE: return
        self.timestamps.append(cycle); self.optimal_power_data.append(optimal_power); self.current_power_data.append(current_power); self.total_replicas_data.append(total_replicas)
        self.line_optimal_power.set_data(self.timestamps, self.optimal_power_data); self.line_current_power.set_data(self.timestamps, self.current_power_data); self.line_total_replicas.set_data(self.timestamps, self.total_replicas_data)
        for dep_id, replicas in individual_replicas.items():
            self.individual_replicas_data[dep_id].append(replicas); self.individual_lines[dep_id].set_data(self.timestamps, self.individual_replicas_data[dep_id])
        for ax in [self.ax1, self.ax1b, self.ax2]: ax.relim(), ax.autoscale_view()
        self.fig.canvas.draw(), self.fig.canvas.flush_events(); plt.pause(0.01)

# --- SOURCE D'ÉNERGIE PAR PALIERS ---
class PowerSource: # ... (Code identique)
    def __init__(self, optimal_power_kw: float, min_plateau_duration: int = 3, max_plateau_duration: int = 8):
        self.optimal_power = optimal_power_kw; self.min_duration = min_plateau_duration; self.max_duration = max_plateau_duration
        self.current_power = self.optimal_power; self.cycles_at_current_level = 0; self.duration_of_current_plateau = random.randint(self.min_duration, self.max_duration)
        logging.info(f"Source d'énergie initialisée. Palier 1: {self.current_power:.2f} kW pour {self.duration_of_current_plateau} cycles.")
    def update_and_get_power(self) -> float:
        self.cycles_at_current_level += 1
        if self.cycles_at_current_level >= self.duration_of_current_plateau:
            self.cycles_at_current_level = 0; self.duration_of_current_plateau = random.randint(self.min_duration, self.max_duration)
            self.current_power = self.optimal_power * random.uniform(0.4, 1.0)
            logging.info(f"Source d'énergie: Changement de palier. Nouvelle puissance: {self.current_power:.2f} kW pour les {self.duration_of_current_plateau} prochains cycles.")
        return self.current_power
    def get_reduction_percentage(self) -> float: return (self.optimal_power - self.current_power) / self.optimal_power

# --- SCALER DÉCLARATIF (structure inspirée de votre code) ---
class DeclarativeScaler:
    def __init__(self, deployments: List[Deployment]):
        self.deployments = deployments
        self.groups = self._build_groups(deployments)

    def _build_groups(self, deployments: List[Deployment]) -> Dict[str, List[Deployment]]:
        groups = {}
        for d in deployments:
            if d.groupe_id:
                if d.groupe_id not in groups:
                    groups[d.groupe_id] = []
                groups[d.groupe_id].append(d)
        return groups

    def _largest_remainder_method(self, shares: List[float], total_int: int) -> List[int]:
        """Répartit un entier total, inspiré de votre implémentation."""
        if not shares or sum(shares) == 0: return [0] * len(shares)
        integer_parts = [math.floor(s) for s in shares]
        remainders = [s - i for s, i in zip(shares, integer_parts)]
        remaining = total_int - sum(integer_parts)
        indices = sorted(range(len(remainders)), key=lambda i: -remainders[i])
        for i in indices[:remaining]:
            integer_parts[i] += 1
        return integer_parts

    def calculate_target_state(self, reduction_percentage: float) -> Dict[str, Any]:
        """Calcule l'état final du cluster basé sur le pourcentage de baisse actuel."""
        logging.info("--- Calcul de l'état cible du cluster ---")

        # Étape 1: Calculer le nombre total de réplicas à retirer DE L'ÉTAT MAXIMAL
        total_max_replicas = sum(d.max for d in self.deployments)
        global_reduction_target = math.ceil(total_max_replicas * reduction_percentage)
        logging.info(f"Pourcentage de baisse: {reduction_percentage:.2%}. Objectif de réduction depuis le max: {global_reduction_target} réplicas")

        if global_reduction_target == 0:
            logging.info("Aucune baisse. L'état cible est l'état optimal (max réplicas).")
            return {'final_state': [{'id': d.id, 'final_replicas': d.max} for d in self.deployments]}

        # Étape 2: Répartir la réduction entre les entités (groupes et déploiements indépendants)
        entities, entity_max_potentials, entity_types = [], [], []
        for group_name, members in self.groups.items():
            entities.append(group_name); entity_max_potentials.append(sum(d.max for d in members)); entity_types.append('group')
        independent_deploys = [d for d in self.deployments if d.groupe_id is None]
        for d in independent_deploys:
            entities.append(d.id); entity_max_potentials.append(d.max); entity_types.append('deployment')
        
        # Utiliser la méthode du plus grand reste pour allouer la réduction aux entités
        entity_shares = [pot / sum(entity_max_potentials) * global_reduction_target for pot in entity_max_potentials]
        entity_allocations = self._largest_remainder_method(entity_shares, global_reduction_target)
        logging.debug(f"Allocations de réduction par entité: {dict(zip(entities, entity_allocations))}")

        # Étape 3: Calculer les réductions voulues par déploiement
        desired_reductions = {d.id: 0 for d in self.deployments}
        for i, entity_name in enumerate(entities):
            alloc = entity_allocations[i]
            if entity_types[i] == 'group':
                members = self.groups[entity_name]
                total_weight = sum(m.poids_ratio for m in members)
                member_shares = [alloc * m.poids_ratio / total_weight if total_weight > 0 else 0 for m in members]
                member_allocations = self._largest_remainder_method(member_shares, alloc)
                for member, member_alloc in zip(members, member_allocations):
                    desired_reductions[member.id] = member_alloc
            else: # C'est un déploiement indépendant
                desired_reductions[entity_name] = alloc
        logging.info(f"Réductions voulues depuis le max: {desired_reductions}")
        
        # Étape 4: Appliquer les contraintes et gérer le déficit
        final_reductions = {}
        total_deficit = 0
        for d in self.deployments:
            actual_reduction = min(desired_reductions.get(d.id, 0), d.max_possible_reduction)
            final_reductions[d.id] = actual_reduction
            total_deficit += (desired_reductions.get(d.id, 0) - actual_reduction)
        
        while total_deficit > 0:
            donors = [d for d in self.deployments if final_reductions[d.id] < d.max_possible_reduction]
            if not donors:
                logging.warning(f"Impossible de reporter le déficit restant de {total_deficit} réplicas.")
                break
            best_donor = max(donors, key=lambda d: (d.max_possible_reduction - final_reductions[d.id]))
            final_reductions[best_donor.id] += 1
            total_deficit -= 1
        
        logging.info(f"Réductions finales depuis le max (après gestion du déficit): {final_reductions}")

        # Étape 5: Calculer le nombre de réplicas final
        final_state = [{'id': d.id, 'final_replicas': d.max - final_reductions.get(d.id, 0)} for d in self.deployments]
        
        return {'final_state': final_state}

# --- SAUVEGARDE DE DONNÉES ---
def save_simulation_data(plotter: RealTimePlotter): # ... (Code identique)
    if not plotter or not plotter.timestamps: logging.warning("Aucune donnée à enregistrer."); return
    timestamp_str = datetime.now().strftime('%Y%m%d_%H%M%S'); base_filename = f"simulation_results_{timestamp_str}"
    # json_filename = f"{base_filename}.json"; json_data = {"simulation_metadata": {"total_cycles": len(plotter.timestamps), "save_time_utc": datetime.utcnow().isoformat()}, "time_series": {"cycle": plotter.timestamps, "optimal_power_kw": plotter.optimal_power_data, "current_power_kw": plotter.current_power_data, "total_replicas": plotter.total_replicas_data, "individual_replicas": plotter.individual_replicas_data}};
    # try:
    #     with open(json_filename, 'w') as f: json.dump(json_data, f, indent=4)
    #     logging.info(f"Données de simulation enregistrées dans : {json_filename}")
    # except IOError as e: logging.error(f"Erreur lors de l'enregistrement du fichier JSON : {e}")
    csv_filename = f"{base_filename}.csv"
    try:
        header = ['cycle', 'optimal_power_kw', 'current_power_kw', 'total_replicas']; header.extend([f"replicas_{dep_id}" for dep_id in plotter.individual_replicas_data.keys()]); rows = [];
        for i in range(len(plotter.timestamps)):
            row = {'cycle': plotter.timestamps[i], 'optimal_power_kw': plotter.optimal_power_data[i], 'current_power_kw': plotter.current_power_data[i], 'total_replicas': plotter.total_replicas_data[i]};
            for dep_id in plotter.individual_replicas_data.keys(): row[f"replicas_{dep_id}"] = plotter.individual_replicas_data[dep_id][i]
            rows.append(row)
        with open(csv_filename, 'w', newline='', encoding='utf-8') as f: writer = csv.DictWriter(f, fieldnames=header); writer.writeheader(); writer.writerows(rows)
        logging.info(f"Données de simulation enregistrées dans : {csv_filename}")
    except IOError as e: logging.error(f"Erreur lors de l'enregistrement du fichier CSV : {e}")

# --- BOUCLE DE SIMULATION PRINCIPALE ---
if __name__ == "__main__":
    SIMULATION_INTERVAL_SECONDS = 3
    
    cluster_topology = [
        Deployment(id="Frontend", min_replicas=2, max_replicas=15, poids_ratio=0.6, groupe_id="Web"),
        Deployment(id="Backend", min_replicas=1, max_replicas=12, poids_ratio=0.4, groupe_id="Web"),
        Deployment(id="Database", min_replicas=3, max_replicas=8),
        Deployment(id="Monitoring", min_replicas=1, max_replicas=5)
    ]
    
    power_source = PowerSource(optimal_power_kw=100.0)
    plotter = RealTimePlotter(deployment_ids=[d.id for d in cluster_topology])
    
    cycle = 0
    try:
        while True:
            cycle += 1
            logging.info(f"===== DÉBUT DU CYCLE N°{cycle} =====")
            
            current_power = power_source.update_and_get_power()
            reduction_percentage = power_source.get_reduction_percentage()
            logging.info(f"Puissance Actuelle: {current_power:.2f} kW / {power_source.optimal_power:.2f} kW")

            scaler = DeclarativeScaler(cluster_topology)
            result = scaler.calculate_target_state(reduction_percentage)
            
            if result and result.get('final_state'):
                for final_state in result['final_state']:
                    # Trouver le déploiement correspondant et mettre à jour son état actuel
                    next(d for d in cluster_topology if d.id == final_state['id']).replicas_actuels = final_state['final_replicas']

            logging.info("--- État du cluster après synchronisation ---")
            total_replicas = sum(d.replicas_actuels for d in cluster_topology)
            individual_replicas = {d.id: d.replicas_actuels for d in cluster_topology}
            for d in cluster_topology:
                logging.info(f"  - {d.id:<15} : {d.replicas_actuels} réplicas (Min: {d.min}, Max: {d.max})")
            logging.info(f"Total des réplicas actifs : {total_replicas}")
            
            plotter.update(cycle, power_source.optimal_power, current_power, total_replicas, individual_replicas)
            
            logging.info(f"Fin du cycle. Prochain cycle dans {SIMULATION_INTERVAL_SECONDS} secondes.")
            time.sleep(SIMULATION_INTERVAL_SECONDS)
            
    except KeyboardInterrupt:
        logging.info("Simulation interrompue par l'utilisateur.")
    finally:
        logging.info("Fin de la simulation. Sauvegarde des données...")
        save_simulation_data(plotter)
        if MATPLOTLIB_AVAILABLE:
            plt.ioff()
            logging.info("Graphique final. Fermez la fenêtre du graphique pour quitter.")
            plt.show()