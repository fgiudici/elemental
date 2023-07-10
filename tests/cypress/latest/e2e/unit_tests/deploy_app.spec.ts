/*
Copyright © 2022 - 2023 SUSE LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

import { Rancher } from '~/support/rancher';
import '~/support/commands';
import filterTests from '~/support/filterTests.js';

filterTests(['main'], () => {
  Cypress.config();
  describe('Deploy application in fresh Elemental Cluster', () => {
    const rancher      = new Rancher();
    const clusterName  = "mycluster"
    beforeEach(() => {
      cy.login();
      cy.visit('/');
    });
    
    it('Deploy Alerting Drivers application', () => {
      rancher.checkClusterStatus(clusterName, 'Active', 600000);
      cy.wait(20000);
      rancher.checkClusterStatus(clusterName, 'Active', 600000);
      // TODO: function to deploy app?
      cy.contains(clusterName)
        .click();
      cy.contains('Apps')
        .click();
      cy.contains('Charts')
        .click();
      cy.contains('Alerting Drivers', {timeout:30000})
        .click();
      cy.contains('.name-logo-install', 'Alerting Drivers', {timeout:30000});
      cy.clickButton('Install');
      cy.contains('.outer-container > .header', 'Alerting Drivers');
      cy.clickButton('Next');
      cy.clickButton('Install');
      cy.contains('SUCCESS: helm install', {timeout:120000});
      cy.reload;
      cy.contains('Deployed rancher-alerting-drivers');
    });
  
    it('Remove Alerting Drivers application', () => {
      rancher.burgerMenuOpenIfClosed();
      cy.contains(clusterName)
        .click();
      cy.contains('Apps')
        .click();
      cy.contains('Installed Apps')
        .click();
      cy.contains('.title', 'Installed Apps', {timeout:20000});
      cy.get('[width="30"] > .checkbox-outer-container')
        .click();
      cy.clickButton('Delete');
      cy.confirmDelete();
      cy.contains('SUCCESS: helm uninstall', {timeout:60000});
      cy.contains('.apps', 'rancher-alerting-drivers')
        .should('not.exist');
    });
  });
});
